package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/planning"
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
	stubPlanFlow(t, nil)
	beforeHead := cliGitOutput(t, repo, "rev-parse", "HEAD")
	beforeIndex := cliGitOutput(t, repo, "diff", "--cached", "--binary")
	beforeConfig := cliGitOutput(t, repo, "config", "--local", "--null", "--list")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "--dry-run"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "this-value") {
		t.Fatalf("sensitive value was printed: %q", stdout.String())
	}
	var result struct {
		Plan         planning.Plan `json:"plan"`
		DryRun       bool          `json:"dry_run"`
		Verification struct {
			Executed bool `json:"executed"`
		} `json:"verification"`
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
	if len(result.Snapshot.Changes) != 2 || len(result.Snapshot.Excluded) != 2 || len(result.Plan.Commits) != 1 || !result.DryRun || result.Verification.Executed {
		t.Fatalf("snapshot=%#v", result.Snapshot)
	}
	if cliGitOutput(t, repo, "rev-parse", "HEAD") != beforeHead || cliGitOutput(t, repo, "diff", "--cached", "--binary") != beforeIndex || cliGitOutput(t, repo, "config", "--local", "--null", "--list") != beforeConfig {
		t.Fatal("dry-run changed HEAD, index, or local remote/config state")
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
	stubPlanFlow(t, nil)

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
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--dry-run"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Count(stdout.String(), "Read all listed candidates?") != 1 || !strings.Contains(stdout.String(), "F001") || !strings.Contains(stdout.String(), "F002") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestMainRegeneratesWithSupplementAndThenApproves(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "main.go", "package main\n", 0o644)
	cliGit(t, repo, "add", "main.go")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "main.go", "package main\n// changed\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	supplements := []string{}
	stubPlanFlow(t, &supplements)
	oldInput := mainInput
	mainInput = strings.NewReader("r\nmake one commit\ny\n")
	t.Cleanup(func() { mainInput = oldInput })

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(supplements) != 2 || supplements[0] != "" || supplements[1] != "make one commit" || strings.Count(stdout.String(), "Create these 1 commits?") != 2 {
		t.Fatalf("supplements=%#v stdout=%q", supplements, stdout.String())
	}
}

func TestMainRejectsPlanAndNoConfirmSkipsPrompt(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		input    string
		wantCode int
		want     string
	}{
		{name: "reject", input: "n\n", wantCode: 3, want: "commit plan rejected"},
		{name: "no confirm", args: []string{"--no-confirm-commit"}, wantCode: 0, want: "Commit confirmation skipped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := cliRepository(t)
			cliWrite(t, repo, "a.txt", "base\n", 0o644)
			cliGit(t, repo, "add", "a.txt")
			cliGit(t, repo, "commit", "-m", "base")
			cliWrite(t, repo, "a.txt", "changed\n", 0o644)
			chdir(t, repo)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			stubPlanFlow(t, nil)
			oldInput := mainInput
			mainInput = strings.NewReader(test.input)
			t.Cleanup(func() { mainInput = oldInput })
			var stdout, stderr bytes.Buffer
			if code := Run(test.args, &stdout, &stderr); code != test.wantCode || !strings.Contains(stdout.String()+stderr.String(), test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func stubPlanFlow(t *testing.T, supplements *[]string) {
	t.Helper()
	previous := planFlow
	planFlow = func(_ context.Context, _ string, snapshot gitstate.Snapshot, _ config.Values, supplement string) (planning.Plan, error) {
		if supplements != nil {
			*supplements = append(*supplements, supplement)
		}
		ids := make([]string, len(snapshot.Changes))
		for index, change := range snapshot.Changes {
			ids[index] = change.ID
		}
		return planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "cli", Summary: "apply changes", FileIDs: ids}}}, nil
	}
	t.Cleanup(func() { planFlow = previous })
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

func cliGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	value, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(value)
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
