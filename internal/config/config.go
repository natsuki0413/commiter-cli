package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/pelletier/go-toml/v2"
)

const MaxFileSize int64 = 1 << 20

type Source string

const (
	SourceDefault Source = "default"
	SourceGlobal  Source = "global"
	SourceRepo    Source = "repo"
	SourceCLI     Source = "cli"
	SourceUnset   Source = "unset"
)

type VerificationCommand struct {
	Name string   `json:"name" toml:"name"`
	Argv []string `json:"argv" toml:"argv"`
	CWD  string   `json:"cwd" toml:"cwd"`
}

type Values struct {
	SchemaVersion     int
	Language          string
	CommitConfirm     bool
	PushEnabled       bool
	PushConfirm       bool
	Model             string
	Endpoint          string
	Context           string
	MaxTokens         int
	Untracked         string
	Include           []string
	Exclude           []string
	Autodetect        bool
	Timeout           int
	Commands          []VerificationCommand
	CommandsSet       bool
	MetricsPersist    bool
	SensitivePatterns []string
}

type Effective struct {
	Values  Values
	Sources map[string]Source
}

type Entry struct {
	Value  any    `json:"value"`
	Source Source `json:"source"`
}

type CLIOverrides struct {
	Language       *string
	CommitConfirm  *bool
	PushEnabled    *bool
	PushConfirm    *bool
	Model          *string
	MetricsPersist *bool
}

type schemaEntry struct {
	kind    string
	allowed map[Source]bool
}

var schema = map[string]schemaEntry{
	"schema_version":                       {"integer", allow(SourceGlobal, SourceRepo)},
	"commit.language":                      {"string", allow(SourceGlobal, SourceRepo)},
	"commit.confirm":                       {"boolean", allow(SourceGlobal)},
	"push.enabled":                         {"boolean", allow(SourceGlobal)},
	"push.confirm":                         {"boolean", allow(SourceGlobal)},
	"llm.model":                            {"string", allow(SourceGlobal, SourceRepo)},
	"llm.endpoint":                         {"string", allow(SourceGlobal)},
	"llm.context":                          {"string", allow(SourceGlobal, SourceRepo)},
	"llm.max_context_tokens":               {"integer", allow(SourceGlobal, SourceRepo)},
	"analysis.untracked":                   {"string", allow(SourceGlobal)},
	"analysis.include":                     {"string_array", allow(SourceGlobal, SourceRepo)},
	"analysis.exclude":                     {"string_array", allow(SourceGlobal, SourceRepo)},
	"verification.autodetect":              {"boolean", allow(SourceRepo)},
	"verification.timeout_seconds":         {"integer", allow(SourceRepo)},
	"verification.commands":                {"commands", allow(SourceRepo)},
	"metrics.persist":                      {"boolean", allow(SourceGlobal)},
	"safety.additional_sensitive_patterns": {"string_array", allow(SourceGlobal)},
}

func allow(sources ...Source) map[Source]bool {
	result := make(map[Source]bool, len(sources))
	for _, source := range sources {
		result[source] = true
	}
	return result
}

func Defaults() Effective {
	sources := make(map[string]Source, len(schema))
	for key := range schema {
		sources[key] = SourceDefault
	}
	sources["verification.commands"] = SourceUnset
	return Effective{
		Values: Values{
			SchemaVersion:     1,
			Language:          "en",
			CommitConfirm:     true,
			PushEnabled:       true,
			PushConfirm:       true,
			Model:             "qwen3.5:4b-q4_K_M",
			Endpoint:          "http://127.0.0.1:11434",
			Context:           "auto",
			MaxTokens:         32768,
			Untracked:         "auto-safe",
			Include:           []string{},
			Exclude:           []string{},
			Autodetect:        true,
			Timeout:           600,
			MetricsPersist:    false,
			SensitivePatterns: []string{},
		},
		Sources: sources,
	}
}

func Resolve(globalPath, repoPath, repoRoot string, cli CLIOverrides) (Effective, error) {
	effective := Defaults()
	if err := applyFile(&effective, globalPath, SourceGlobal, repoRoot); err != nil {
		return Effective{}, err
	}
	if err := validateValues(effective.Values, repoRoot); err != nil {
		return Effective{}, fmt.Errorf("invalid global configuration: %w", err)
	}
	if err := applyFile(&effective, repoPath, SourceRepo, repoRoot); err != nil {
		return Effective{}, err
	}
	if err := validateValues(effective.Values, repoRoot); err != nil {
		return Effective{}, fmt.Errorf("invalid repo configuration: %w", err)
	}
	applyCLI(&effective, cli)
	if err := validateValues(effective.Values, repoRoot); err != nil {
		return Effective{}, fmt.Errorf("invalid CLI configuration: %w", err)
	}
	return effective, nil
}

func applyFile(effective *Effective, path string, source Source, repoRoot string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot open %s configuration", source)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("cannot inspect %s configuration", source)
	}
	if info.Size() > MaxFileSize {
		return fmt.Errorf("%s configuration exceeds %d bytes", source, MaxFileSize)
	}

	var raw map[string]any
	decoder := toml.NewDecoder(io.LimitReader(file, MaxFileSize+1))
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("invalid TOML in %s configuration", source)
	}
	flat := make(map[string]any)
	if err := flatten("", raw, flat); err != nil {
		return fmt.Errorf("invalid %s configuration: %w", source, err)
	}
	keys := make([]string, 0, len(flat))
	for key := range flat {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry, ok := schema[key]
		if !ok {
			return fmt.Errorf("unknown configuration key %q", key)
		}
		if !entry.allowed[source] {
			return fmt.Errorf("configuration key %q is not allowed in %s configuration", key, source)
		}
		if err := applyValue(effective, key, entry.kind, flat[key], source, repoRoot); err != nil {
			return err
		}
	}
	return nil
}

func flatten(prefix string, table map[string]any, flat map[string]any) error {
	for key, value := range table {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if path == "verification.commands" {
			flat[path] = value
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			if !isSchemaTable(path) {
				return fmt.Errorf("unknown configuration key %q", path)
			}
			if err := flatten(path, nested, flat); err != nil {
				return err
			}
			continue
		}
		flat[path] = value
	}
	return nil
}

func isSchemaTable(path string) bool {
	prefix := path + "."
	for key := range schema {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func applyValue(e *Effective, key, kind string, raw any, source Source, repoRoot string) error {
	if key == "verification.commands" {
		commands, err := parseCommands(raw, repoRoot)
		if err != nil {
			return fmt.Errorf("invalid %q: %w", key, err)
		}
		e.Values.Commands = commands
		e.Values.CommandsSet = true
		e.Sources[key] = source
		return nil
	}

	switch kind {
	case "integer":
		value, ok := integer(raw)
		if !ok {
			return typeError(key, kind)
		}
		switch key {
		case "schema_version":
			e.Values.SchemaVersion = value
		case "llm.max_context_tokens":
			e.Values.MaxTokens = value
		case "verification.timeout_seconds":
			e.Values.Timeout = value
		}
	case "string":
		value, ok := raw.(string)
		if !ok {
			return typeError(key, kind)
		}
		switch key {
		case "commit.language":
			e.Values.Language = value
		case "llm.model":
			e.Values.Model = value
		case "llm.endpoint":
			e.Values.Endpoint = value
		case "llm.context":
			e.Values.Context = value
		case "analysis.untracked":
			e.Values.Untracked = value
		}
	case "boolean":
		value, ok := raw.(bool)
		if !ok {
			return typeError(key, kind)
		}
		switch key {
		case "commit.confirm":
			e.Values.CommitConfirm = value
		case "push.enabled":
			e.Values.PushEnabled = value
		case "push.confirm":
			e.Values.PushConfirm = value
		case "verification.autodetect":
			e.Values.Autodetect = value
		case "metrics.persist":
			e.Values.MetricsPersist = value
		}
	case "string_array":
		value, ok := stringArray(raw)
		if !ok {
			return typeError(key, kind)
		}
		switch key {
		case "analysis.include":
			e.Values.Include = value
		case "analysis.exclude":
			e.Values.Exclude = value
		case "safety.additional_sensitive_patterns":
			e.Values.SensitivePatterns = value
		}
	default:
		return fmt.Errorf("unsupported schema type for %q", key)
	}
	e.Sources[key] = source
	return nil
}

func applyCLI(e *Effective, cli CLIOverrides) {
	if cli.Language != nil {
		e.Values.Language, e.Sources["commit.language"] = *cli.Language, SourceCLI
	}
	if cli.CommitConfirm != nil {
		e.Values.CommitConfirm, e.Sources["commit.confirm"] = *cli.CommitConfirm, SourceCLI
	}
	if cli.PushEnabled != nil {
		e.Values.PushEnabled, e.Sources["push.enabled"] = *cli.PushEnabled, SourceCLI
	}
	if cli.PushConfirm != nil {
		e.Values.PushConfirm, e.Sources["push.confirm"] = *cli.PushConfirm, SourceCLI
	}
	if cli.Model != nil {
		e.Values.Model, e.Sources["llm.model"] = *cli.Model, SourceCLI
	}
	if cli.MetricsPersist != nil {
		e.Values.MetricsPersist, e.Sources["metrics.persist"] = *cli.MetricsPersist, SourceCLI
	}
}

func validateValues(v Values, repoRoot string) error {
	if v.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version")
	}
	if v.Language != "en" && v.Language != "ja" {
		return fmt.Errorf("commit.language must be en or ja")
	}
	if v.Context != "auto" && v.Context != "8k" && v.Context != "16k" && v.Context != "32k" {
		return fmt.Errorf("llm.context is not supported")
	}
	if v.MaxTokens != 8192 && v.MaxTokens != 16384 && v.MaxTokens != 32768 {
		return fmt.Errorf("llm.max_context_tokens is not supported")
	}
	if v.Timeout <= 0 {
		return fmt.Errorf("verification.timeout_seconds must be positive")
	}
	if v.Model == "" {
		return fmt.Errorf("llm.model must not be empty")
	}
	if err := validateEndpoint(v.Endpoint); err != nil {
		return err
	}
	for _, pattern := range append(append([]string{}, v.Include...), v.Exclude...) {
		if err := validateRepoPattern(pattern); err != nil {
			return err
		}
	}
	if v.CommandsSet {
		if len(v.Commands) == 0 {
			return fmt.Errorf("verification.commands must contain at least one command")
		}
		for _, command := range v.Commands {
			if err := validateCommandCWD(command.CWD, repoRoot); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateEndpoint(raw string) error {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "http" || endpoint.Host == "" {
		return fmt.Errorf("llm.endpoint must be a loopback HTTP URL")
	}
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("llm.endpoint must be a loopback HTTP URL")
	}
	return nil
}

func validateRepoPattern(pattern string) error {
	if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("analysis glob must be repo-root relative")
	}
	for _, part := range strings.Split(filepath.ToSlash(pattern), "/") {
		if part == ".." {
			return fmt.Errorf("analysis glob must not escape the repo root")
		}
	}
	if _, err := doublestar.Match(pattern, "validation-target"); err != nil {
		return fmt.Errorf("analysis glob is invalid")
	}
	return nil
}

func parseCommands(raw any, repoRoot string) ([]VerificationCommand, error) {
	items, ok := raw.([]map[string]any)
	if !ok {
		generic, genericOK := raw.([]any)
		if !genericOK {
			return nil, fmt.Errorf("must be an array of tables")
		}
		items = make([]map[string]any, 0, len(generic))
		for _, item := range generic {
			table, tableOK := item.(map[string]any)
			if !tableOK {
				return nil, fmt.Errorf("must contain tables")
			}
			items = append(items, table)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("must contain at least one command")
	}
	commands := make([]VerificationCommand, 0, len(items))
	for _, item := range items {
		for key := range item {
			if key != "name" && key != "argv" && key != "cwd" {
				return nil, fmt.Errorf("contains unknown field %q", key)
			}
		}
		name, nameOK := item["name"].(string)
		argv, argvOK := stringArray(item["argv"])
		cwd, cwdOK := item["cwd"].(string)
		if !nameOK || !argvOK || !cwdOK || name == "" || len(argv) == 0 || cwd == "" {
			return nil, fmt.Errorf("requires non-empty name, argv, and cwd")
		}
		commands = append(commands, VerificationCommand{Name: name, Argv: argv, CWD: cwd})
	}
	return commands, nil
}

func validateCommandCWD(cwd, repoRoot string) error {
	if repoRoot == "" {
		return fmt.Errorf("cannot validate verification cwd without a repository root")
	}
	root, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return fmt.Errorf("cannot resolve repository root")
	}
	candidate := cwd
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return fmt.Errorf("verification cwd cannot be resolved")
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("verification cwd must stay inside the repo root")
	}
	return nil
}

func integer(raw any) (int, bool) {
	switch value := raw.(type) {
	case int64:
		return int(value), int64(int(value)) == value
	case int:
		return value, true
	default:
		return 0, false
	}
}

func stringArray(raw any) ([]string, bool) {
	if values, ok := raw.([]string); ok {
		return append([]string{}, values...), true
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.(string)
		if !ok {
			return nil, false
		}
		result = append(result, item)
	}
	return result, true
}

func typeError(key, expected string) error {
	return fmt.Errorf("configuration key %q must have type %s", key, expected)
}

func (e Effective) Entries() map[string]Entry {
	v := e.Values
	values := map[string]any{
		"schema_version":                       v.SchemaVersion,
		"commit.language":                      v.Language,
		"commit.confirm":                       v.CommitConfirm,
		"push.enabled":                         v.PushEnabled,
		"push.confirm":                         v.PushConfirm,
		"llm.model":                            v.Model,
		"llm.endpoint":                         v.Endpoint,
		"llm.context":                          v.Context,
		"llm.max_context_tokens":               v.MaxTokens,
		"analysis.untracked":                   v.Untracked,
		"analysis.include":                     v.Include,
		"analysis.exclude":                     v.Exclude,
		"verification.autodetect":              v.Autodetect,
		"verification.timeout_seconds":         v.Timeout,
		"verification.commands":                v.Commands,
		"metrics.persist":                      v.MetricsPersist,
		"safety.additional_sensitive_patterns": v.SensitivePatterns,
	}
	entries := make(map[string]Entry, len(values))
	for key, value := range values {
		entries[key] = Entry{Value: value, Source: e.Sources[key]}
	}
	return entries
}

func SortedKeys() []string {
	keys := make([]string, 0, len(schema))
	for key := range schema {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
