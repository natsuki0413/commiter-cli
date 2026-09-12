package pushexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/interaction"
)

func TestExecutePushesAllOutgoingCommitsOnceToUpstream(t *testing.T) {
	repo, remote := pushRepository(t)
	writeAndCommit(t, repo, "existing.txt", "existing outgoing\n", "existing")
	writeAndCommit(t, repo, "current.txt", "current run\n", "current")

	target := interaction.ResolvePushTarget(repo)
	if !target.Resolved || target.SetUpstream {
		t.Fatalf("target=%#v", target)
	}
	if err := Execute(context.Background(), repo, target); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, remote, "rev-list", "--count", "refs/heads/main"); got != "3" {
		t.Fatalf("remote commit count=%q", got)
	}
}

func TestExecuteSetsUpstreamOnlyForSingleRemoteFallback(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "feature")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "remote", "add", "origin", remote)
	writeAndCommit(t, repo, "change.txt", "change\n", "change")

	target := interaction.ResolvePushTarget(repo)
	if !target.Resolved || !target.SetUpstream {
		t.Fatalf("target=%#v", target)
	}
	if err := Execute(context.Background(), repo, target); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, repo, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/feature" {
		t.Fatalf("upstream=%q", got)
	}
}

func TestExecuteKeepsFailureOutputPrivate(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	target := interaction.PushTarget{Remote: "missing", Branch: "main", Resolved: true}
	if err := Execute(context.Background(), repo, target); !errors.Is(err, ErrPush) || strings.Contains(err.Error(), repo) {
		t.Fatalf("error=%v", err)
	}
}

func pushRepository(t *testing.T) (string, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "remote", "add", "origin", remote)
	writeAndCommit(t, repo, "base.txt", "base\n", "base")
	runGit(t, repo, "push", "--set-upstream", "origin", "main")
	return repo, remote
}

func writeAndCommit(t *testing.T, repo, name, contents, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", name)
	runGit(t, repo, "commit", "-m", message)
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	commandArgs := append([]string{}, args...)
	if repo != "" {
		commandArgs = append([]string{"-C", repo}, commandArgs...)
	}
	command := exec.Command("git", commandArgs...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
