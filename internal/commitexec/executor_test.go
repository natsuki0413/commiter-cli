package commitexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/planning"
)

func TestExecutePreservesOutOfScopeIndexAndCreatesPlanOrder(t *testing.T) {
	repo := t.TempDir()
	gitExec(t, repo, "init", "-b", "main")
	gitExec(t, repo, "config", "user.email", "test@example.invalid")
	gitExec(t, repo, "config", "user.name", "Test User")
	writeFile(t, repo, "a.txt", "a0\n")
	writeFile(t, repo, "b.txt", "b0\n")
	writeFile(t, repo, "outside.txt", "outside0\n")
	gitExec(t, repo, "add", ".")
	gitExec(t, repo, "commit", "-m", "base")
	writeFile(t, repo, "a.txt", "a1\n")
	writeFile(t, repo, "b.txt", "b1\n")
	writeFile(t, repo, "outside.txt", "outside1\n")
	gitExec(t, repo, "add", "outside.txt")

	snapshot, err := gitstate.Collect(repo, gitstate.Options{})
	if err != nil {
		if value, ok := err.(*Error); ok {
			t.Fatalf("execute: %v paths=%#v hash=%s", err, value.Paths, value.CommitHash)
		}
		t.Fatal(err)
	}
	byPath := map[string]gitstate.Change{}
	for _, change := range snapshot.Changes {
		byPath[*change.NewPath] = change
	}
	plan := planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{
		{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{byPath["a.txt"].ID}},
		{Type: "fix", Scope: "b", Summary: "update b", FileIDs: []string{byPath["b.txt"].ID}},
	}}
	result, err := Execute(Options{Root: repo, Changes: []gitstate.Change{byPath["a.txt"], byPath["b.txt"]}, Plan: plan})
	if err != nil {
		if value, ok := err.(*Error); ok {
			t.Fatalf("execute: %v paths=%#v hash=%s", err, value.Paths, value.CommitHash)
		}
		t.Fatal(err)
	}
	if len(result.Hashes) != 2 {
		t.Fatalf("hashes = %#v", result.Hashes)
	}
	if got := gitExec(t, repo, "log", "--format=%s", "-2"); got != "fix(b): update b\nfix(a): update a\n" {
		t.Fatalf("log = %q", got)
	}
	if got := gitExec(t, repo, "diff", "--cached", "--name-only"); got != "outside.txt\n" {
		t.Fatalf("outside index = %q", got)
	}
}

func writeFile(t *testing.T, repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
func gitExec(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	value, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, value)
	}
	return string(value)
}
