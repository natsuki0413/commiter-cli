package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/config"
)

const (
	SourceRepoConfig            = "repo_config"
	SourcePackageJSONAutodetect = "package_json_autodetect"
)

var scriptOrder = []string{"lint", "typecheck", "test", "build"}

type Command struct {
	Name                     string           `json:"name"`
	CWD                      string           `json:"cwd"`
	Argv                     []string         `json:"argv"`
	ManifestPath             string           `json:"manifest_path,omitempty"`
	ScriptName               string           `json:"script_name,omitempty"`
	ScriptBody               string           `json:"script_body,omitempty"`
	ImplicitLifecycleScripts []ManifestScript `json:"implicit_lifecycle_scripts,omitempty"`
}

type ManifestScript struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type Definition struct {
	SchemaVersion int       `json:"schema_version"`
	SourceType    string    `json:"source_type"`
	Commands      []Command `json:"commands"`
}

func Resolve(root string, values config.Values) (*Definition, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve repository root")
	}
	if values.CommandsSet {
		commands := make([]Command, 0, len(values.Commands))
		for _, configured := range values.Commands {
			cwd, err := normalizedCWD(canonicalRoot, configured.CWD)
			if err != nil {
				return nil, err
			}
			commands = append(commands, Command{
				Name: configured.Name,
				CWD:  cwd,
				Argv: append([]string{}, configured.Argv...),
			})
		}
		return &Definition{SchemaVersion: 1, SourceType: SourceRepoConfig, Commands: commands}, nil
	}
	if !values.Autodetect {
		return nil, nil
	}
	return autodetect(canonicalRoot)
}

func (d Definition) CanonicalJSON() ([]byte, error) {
	if d.SchemaVersion != 1 || len(d.Commands) == 0 {
		return nil, fmt.Errorf("invalid verification definition")
	}
	type canonicalDefinition struct {
		SchemaVersion int    `json:"schema_version"`
		SourceType    string `json:"source_type"`
		Commands      any    `json:"commands"`
	}
	type canonicalRepoCommand struct {
		Name string   `json:"name"`
		CWD  string   `json:"cwd"`
		Argv []string `json:"argv"`
	}
	type canonicalAutodetectCommand struct {
		Name                     string           `json:"name"`
		CWD                      string           `json:"cwd"`
		Argv                     []string         `json:"argv"`
		ManifestPath             string           `json:"manifest_path"`
		ScriptName               string           `json:"script_name"`
		ScriptBody               string           `json:"script_body"`
		ImplicitLifecycleScripts []ManifestScript `json:"implicit_lifecycle_scripts,omitempty"`
	}

	canonical := canonicalDefinition{SchemaVersion: d.SchemaVersion, SourceType: d.SourceType}
	switch d.SourceType {
	case SourceRepoConfig:
		commands := make([]canonicalRepoCommand, 0, len(d.Commands))
		for _, command := range d.Commands {
			if !validCommonCommand(command) || command.ManifestPath != "" || command.ScriptName != "" || command.ScriptBody != "" || len(command.ImplicitLifecycleScripts) != 0 {
				return nil, fmt.Errorf("invalid verification definition")
			}
			commands = append(commands, canonicalRepoCommand{Name: command.Name, CWD: command.CWD, Argv: command.Argv})
		}
		canonical.Commands = commands
	case SourcePackageJSONAutodetect:
		commands := make([]canonicalAutodetectCommand, 0, len(d.Commands))
		for _, command := range d.Commands {
			if !validCommonCommand(command) || command.ManifestPath == "" || command.ScriptName == "" {
				return nil, fmt.Errorf("invalid verification definition")
			}
			commands = append(commands, canonicalAutodetectCommand{
				Name:                     command.Name,
				CWD:                      command.CWD,
				Argv:                     command.Argv,
				ManifestPath:             command.ManifestPath,
				ScriptName:               command.ScriptName,
				ScriptBody:               command.ScriptBody,
				ImplicitLifecycleScripts: command.ImplicitLifecycleScripts,
			})
		}
		canonical.Commands = commands
	default:
		return nil, fmt.Errorf("invalid verification definition")
	}
	return json.Marshal(canonical)
}

func validCommonCommand(command Command) bool {
	return command.Name != "" && command.CWD != "" && len(command.Argv) > 0
}

func (d Definition) Hash() (string, error) {
	canonical, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (d Definition) Argv() [][]string {
	result := make([][]string, 0, len(d.Commands))
	for _, command := range d.Commands {
		result = append(result, append([]string{}, command.Argv...))
	}
	return result
}

type packageManifest struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

func autodetect(root string) (*Definition, error) {
	manifestPath := filepath.Join(root, "package.json")
	contents, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read package.json")
	}
	var manifest packageManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return nil, fmt.Errorf("invalid package.json")
	}
	manager, ok, err := packageManager(root, manifest.PackageManager)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	commands := make([]Command, 0, len(scriptOrder))
	for _, name := range scriptOrder {
		body, exists := manifest.Scripts[name]
		if !exists {
			continue
		}
		command := Command{
			Name:         name,
			CWD:          ".",
			Argv:         autodetectArgv(manager, name),
			ManifestPath: "package.json",
			ScriptName:   name,
			ScriptBody:   body,
		}
		if runsImplicitLifecycle(manager, manifest.PackageManager) {
			command.ImplicitLifecycleScripts = implicitLifecycleScripts(manifest.Scripts, name)
		}
		commands = append(commands, command)
	}
	if len(commands) == 0 {
		return nil, nil
	}
	return &Definition{SchemaVersion: 1, SourceType: SourcePackageJSONAutodetect, Commands: commands}, nil
}

func autodetectArgv(manager, name string) []string {
	switch manager {
	case "npm":
		return []string{"npm", "run", "--ignore-scripts", name}
	case "pnpm":
		return []string{"pnpm", "--config.enable-pre-post-scripts=false", "run", name}
	default:
		return []string{manager, "run", name}
	}
}

func runsImplicitLifecycle(manager, declared string) bool {
	if manager == "bun" {
		return true
	}
	if manager != "yarn" {
		return false
	}
	declaredManager, version, found := strings.Cut(declared, "@")
	if !found || declaredManager != "yarn" {
		return true
	}
	if separator := strings.IndexAny(version, ".-+"); separator >= 0 {
		version = version[:separator]
	}
	major, err := strconv.Atoi(version)
	return err != nil || major < 2
}

func implicitLifecycleScripts(scripts map[string]string, name string) []ManifestScript {
	result := make([]ManifestScript, 0, 2)
	for _, lifecycleName := range []string{"pre" + name, "post" + name} {
		if body, exists := scripts[lifecycleName]; exists {
			result = append(result, ManifestScript{Name: lifecycleName, Body: body})
		}
	}
	return result
}

func packageManager(root, declared string) (string, bool, error) {
	candidates := map[string]bool{}
	if declared != "" {
		manager := strings.SplitN(declared, "@", 2)[0]
		if !supportedManager(manager) {
			return "", false, nil
		}
		candidates[manager] = true
	}
	markers := map[string][]string{
		"npm":  {"package-lock.json", "npm-shrinkwrap.json"},
		"yarn": {"yarn.lock"},
		"pnpm": {"pnpm-lock.yaml"},
		"bun":  {"bun.lock", "bun.lockb"},
	}
	for manager, names := range markers {
		for _, name := range names {
			_, err := os.Stat(filepath.Join(root, name))
			if err == nil {
				candidates[manager] = true
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return "", false, fmt.Errorf("cannot inspect package manager marker")
			}
		}
	}
	if len(candidates) != 1 {
		return "", false, nil
	}
	for manager := range candidates {
		return manager, true, nil
	}
	return "", false, nil
}

func supportedManager(manager string) bool {
	switch manager {
	case "npm", "yarn", "pnpm", "bun":
		return true
	default:
		return false
	}
}

func normalizedCWD(root, cwd string) (string, error) {
	candidate := cwd
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	canonical, err := canonicalDirectory(candidate)
	if err != nil {
		return "", fmt.Errorf("verification cwd cannot be resolved")
	}
	relative, err := filepath.Rel(root, canonical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("verification cwd must stay inside the repo root")
	}
	return filepath.ToSlash(relative), nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return filepath.Clean(canonical), nil
}
