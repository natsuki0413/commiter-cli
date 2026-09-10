package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunCollectsSnapshotThroughExistingCLIBoundaries(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "main.go", "package main\n", 0o644)
	cliGit(t, repo, "add", "main.go")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "main.go", "package main\n// changed\n", 0o644)
	cliWrite(t, repo, "new.txt", "new\n", 0o644)
	cliWrite(t, repo, ".env", "DO_NOT_PRINT=this-value\n", 0o600)
	cliWrite(t, repo, "auth.json", "DO_NOT_PRINT=this-value\n", 0o600)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "--dry-run"}, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "this-value") {
		t.Fatalf("sensitive value was printed: %q", stdout.String())
	}
	var result struct {
		Snapshot struct {
			Changes []struct {
				Path *string `json:"new_path"`
			} `json:"changes"`
			Excluded []struct {
				Path string `json:"path"`
			} `json:"excluded"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("JSON=%q error=%v", stdout.String(), err)
	}
	if len(result.Snapshot.Changes) != 2 || len(result.Snapshot.Excluded) != 2 {
		t.Fatalf("snapshot=%#v", result.Snapshot)
	}
}

func TestMainReturnsSafetyExitForDetachedHead(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "README.md", "base\n", 0o644)
	cliGit(t, repo, "add", "README.md")
	cliGit(t, repo, "commit", "-m", "base")
	cliGit(t, repo, "checkout", "--detach")
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 4 || !strings.Contains(stderr.String(), "detached HEAD") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestMainReturnsSuccessWhenThereAreNoTargetChanges(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "README.md", "base\n", 0o644)
	cliGit(t, repo, "add", "README.md")
	cliGit(t, repo, "commit", "-m", "base")
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "No target changes.") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestMainPromptsCandidatesOnceAndAcceptsAll(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "README.md", "base\n", 0o644)
	cliGit(t, repo, "add", "README.md")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "auth.json", "local-only\n", 0o600)
	cliWrite(t, repo, "tokens.toml", "local-only\n", 0o600)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldInput := mainInput
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--dry-run"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "not implemented") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Count(stdout.String(), "Read all listed candidates?") != 1 || !strings.Contains(stdout.String(), "F001") || !strings.Contains(stdout.String(), "F002") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func cliRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	cliGit(t, repo, "init", "-b", "main")
	cliGit(t, repo, "config", "user.name", "Test User")
	cliGit(t, repo, "config", "user.email", "test@example.invalid")
	return repo
}

func cliGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func cliWrite(t *testing.T, repo, path, content string, mode os.FileMode) {
	t.Helper()
	absolute := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, directory string) {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(current) })
}
