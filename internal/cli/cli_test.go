package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/trust"
)

func TestVersionHumanAndJSON(t *testing.T) {
	oldVersion := Version
	Version = "test-version"
	t.Cleanup(func() { Version = oldVersion })

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "commiter test-version\n" || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}

	stdout.Reset()
	if code := Run([]string{"--json", "version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("JSON code = %d", code)
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["version"] != "test-version" {
		t.Fatalf("JSON = %q, error = %v", stdout.String(), err)
	}
}

func TestJSONRestrictionIsUsageErrorAndDoesNotMixStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json"}, &stdout, &stderr)
	if code != 2 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var result struct {
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.ExitCode != 2 {
		t.Fatalf("JSON = %q, error = %v", stdout.String(), err)
	}
}

func TestJSONDoctorPreservesImplementationAndUsageExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"valid but unimplemented", []string{"--json", "doctor"}, 1},
		{"invalid argument", []string{"--json", "doctor", "nonsense"}, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(test.args, &stdout, &stderr)
			if code != test.wantCode || stderr.Len() != 0 {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			var result struct {
				ExitCode int `json:"exit_code"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.ExitCode != test.wantCode {
				t.Fatalf("JSON = %q, error = %v", stdout.String(), err)
			}
		})
	}
}

func TestKnownButUnimplementedCommandsDoNotSucceed(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"setup", []string{"setup"}},
		{"setup update model", []string{"setup", "--update-model"}},
		{"doctor", []string{"doctor"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(test.args, &stdout, &stderr); code != 1 {
				t.Fatalf("%v code = %d, stderr = %q", test.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "not implemented") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestUnimplementedCommandArgumentsAreValidated(t *testing.T) {
	tests := [][]string{
		{"setup", "--garbage"},
		{"setup", "--update-model", "extra"},
		{"doctor", "nonsense"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr); code != 2 {
				t.Fatalf("%v code = %d, stderr = %q", args, code, stderr.String())
			}
		})
	}
}

func TestConfigShowJSONUsesOnlyStdout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "config", "show", "--effective"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["config"] == nil {
		t.Fatalf("JSON = %q, error = %v", stdout.String(), err)
	}
}

func TestConfigPathAndInitGlobal(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	wantPath := filepath.Join(configHome, "commiter", "config.toml")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "config", "path", "--global"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("path code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var pathResult map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &pathResult); err != nil || pathResult["path"] != wantPath {
		t.Fatalf("path JSON = %q, error = %v", stdout.String(), err)
	}

	stdout.Reset()
	code = Run([]string{"config", "init", "--global"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init code = %d, stderr = %q", code, stderr.String())
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("global config mode = %o", info.Mode().Perm())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "init", "--global"}, &stdout, &stderr); code != 2 {
		t.Fatalf("duplicate init code = %d, stderr = %q", code, stderr.String())
	}
}

func TestConfigShowAppliesCLIOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--language", "ja", "--json", "config", "show"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Config map[string]struct {
			Value  any    `json:"value"`
			Source string `json:"source"`
		} `json:"config"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	entry := result.Config["commit.language"]
	if entry.Value != "ja" || entry.Source != "cli" {
		t.Fatalf("commit.language = %#v", entry)
	}
}

func TestConfigErrorDoesNotExposeRawValue(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	directory := filepath.Join(configHome, "commiter")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.toml"), []byte("[commit]\nconfirm = \"RAW_SECRET\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"config", "show"}, &stdout, &stderr)
	if code != 2 || strings.Contains(stderr.String(), "RAW_SECRET") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestTrustListJSONIsReadOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "trust", "list"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var result struct {
		Trust []any `json:"trust"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Trust) != 0 {
		t.Fatalf("JSON = %q, error = %v", stdout.String(), err)
	}
}

func TestTrustListShowsCanonicalRepoHashSourceAndArgv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	repo := t.TempDir()
	store := trust.New(filepath.Join(stateHome, "commiter"))
	if err := store.Approve(trust.Entry{
		RepoPath:       repo,
		DefinitionHash: "definition-hash",
		SourceType:     "repo_config",
		Commands:       [][]string{{"echo", "a b"}, {"echo", "a", "b"}, {"printf", "", "line\nbreak"}},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "trust", "list"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var result struct {
		Trust []trust.Entry `json:"trust"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trust) != 1 || result.Trust[0].RepoPath != canonical || result.Trust[0].DefinitionHash != "definition-hash" || result.Trust[0].SourceType != "repo_config" {
		t.Fatalf("trust = %#v", result.Trust)
	}
	if !reflect.DeepEqual(result.Trust[0].Commands, [][]string{{"echo", "a b"}, {"echo", "a", "b"}, {"printf", "", "line\nbreak"}}) {
		t.Fatalf("argv = %#v", result.Trust[0].Commands)
	}

	stdout.Reset()
	code = Run([]string{"trust", "list"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `argv=[[\"echo\",\"a b\"],[\"echo\",\"a\",\"b\"],[\"printf\",\"\",\"line\\nbreak\"]]`) {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestCorruptTrustStateIsInternalForListAndRevoke(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	stateDir := filepath.Join(stateHome, "commiter")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "trust.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()

	for _, args := range [][]string{{"trust", "list"}, {"trust", "revoke", repo}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(args, &stdout, &stderr)
			if code != 1 || !strings.Contains(stderr.String(), "invalid trust state") {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}
