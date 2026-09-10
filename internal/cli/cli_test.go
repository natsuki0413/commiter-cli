package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/exitcode"
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

func TestJSONDoctorIsStableAndReadOnly(t *testing.T) {
	configHome, stateHome := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "doctor"}, &stdout, &stderr)
	if code == 2 || stderr.Len() != 0 {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	var result struct {
		Doctor   map[string]map[string]any `json:"doctor"`
		ReadOnly bool                      `json:"read_only"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON = %q, error = %v", stdout.String(), err)
	}
	if !result.ReadOnly || result.Doctor["git"] == nil || result.Doctor["config"] == nil || result.Doctor["ollama"] == nil {
		t.Fatalf("doctor JSON = %#v", result)
	}
	entries, err := os.ReadDir(configHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("doctor created configuration entries: %v", entries)
	}
	stateEntries, err := os.ReadDir(stateHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(stateEntries) != 0 {
		t.Fatalf("doctor created trust/state entries: %v", stateEntries)
	}
}

func TestSetupConfirmationRejectionDoesNotInvokeOperation(t *testing.T) {
	tests := []struct {
		name            string
		ollamaErr, brew bool
	}{{"install", true, true}, {"daemon", false, true}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldLookPath, oldCommand, oldConfirm := lookPath, commandFactory, confirmFunc
			t.Cleanup(func() { lookPath, commandFactory, confirmFunc = oldLookPath, oldCommand, oldConfirm })
			commandCalled := false
			lookPath = func(name string) (string, error) {
				if name == "ollama" && test.ollamaErr {
					return "", os.ErrNotExist
				}
				if name == "brew" && test.brew {
					return "/brew", nil
				}
				return "/" + name, nil
			}
			commandFactory = func(string, ...string) *exec.Cmd { commandCalled = true; return exec.Command("false") }
			confirmFunc = func(string) bool { return false }
			if code := Run([]string{"setup"}, io.Discard, io.Discard); code != exitcode.Canceled || commandCalled {
				t.Fatalf("code=%d commandCalled=%v", code, commandCalled)
			}
		})
	}
}

func TestSetupUpdateModelPullsOnlyAfterApproval(t *testing.T) {
	pulled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/version":
			_, _ = io.WriteString(w, `{"version":"0.31.2"}`)
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"qwen3.5:4b-q4_K_M"}]}`)
		case "/api/pull":
			pulled = true
			_, _ = io.WriteString(w, `{"status":"success"}`)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(configHome, "commiter"), 0o700); err != nil {
		t.Fatal(err)
	}
	configText := "[llm]\nmodel = \"qwen3.5:4b-q4_K_M\"\nendpoint = \"" + server.URL + "\"\n"
	if err := os.WriteFile(filepath.Join(configHome, "commiter", "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLookPath, oldConfirm := lookPath, confirmFunc
	t.Cleanup(func() { lookPath, confirmFunc = oldLookPath, oldConfirm })
	lookPath = func(name string) (string, error) { return "/" + name, nil }
	promptDisplayed := false
	confirmFunc = func(prompt string) bool {
		promptDisplayed = strings.Contains(prompt, "Pull/update model")
		return promptDisplayed
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"setup", "--update-model"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !promptDisplayed || !pulled {
		t.Fatalf("promptDisplayed=%v pulled=%v", promptDisplayed, pulled)
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
