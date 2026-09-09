package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestKnownButUnimplementedCommandsDoNotSucceed(t *testing.T) {
	for _, command := range []string{"setup", "doctor"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run([]string{command}, &stdout, &stderr); code == 0 {
				t.Fatalf("%s unexpectedly succeeded", command)
			}
			if !strings.Contains(stderr.String(), "not implemented") {
				t.Fatalf("stderr = %q", stderr.String())
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
