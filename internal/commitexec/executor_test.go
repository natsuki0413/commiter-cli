package commitexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

func TestExecuteKeepsCommittedWorkAndRestoresOnlyOutsideIndexOnLaterHashFailure(t *testing.T) {
	repo := newRepo(t, "a.txt", "b.txt", "outside.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	writeFile(t, repo, "b.txt", "b1\n")
	writeFile(t, repo, "outside.txt", "outside1\n")
	gitExec(t, repo, "add", "outside.txt")
	snapshot := collect(t, repo)
	byPath := changesByPath(snapshot)
	writeHook(t, repo, "pre-commit", "#!/bin/sh\ncase \"$(git diff --cached --name-only)\" in *a.txt*) printf 'changed by hook\\n' > b.txt;; esac\n")

	result, err := Execute(Options{Root: repo, Changes: []gitstate.Change{byPath["a.txt"], byPath["b.txt"]}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{
		{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{byPath["a.txt"].ID}},
		{Type: "fix", Scope: "b", Summary: "update b", FileIDs: []string{byPath["b.txt"].ID}},
	}}})
	var failure *Error
	if !errors.As(err, &failure) || len(result.Hashes) != 1 || !failure.Restored {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
	if got := gitExec(t, repo, "log", "-1", "--format=%s"); got != "fix(a): update a\n" {
		t.Fatalf("latest commit = %q", got)
	}
	if got := gitExec(t, repo, "diff", "--cached", "--name-only"); got != "outside.txt\n" {
		t.Fatalf("restored index = %q", got)
	}
}

func TestExecuteCommitsRenameAsOldAndNewPath(t *testing.T) {
	repo := newRepo(t, "old.txt")
	gitExec(t, repo, "mv", "old.txt", "new.txt")
	snapshot := collect(t, repo)
	change := snapshot.Changes[0]
	result, err := Execute(Options{Root: repo, Changes: []gitstate.Change{change}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "refactor", Scope: "files", Summary: "rename file", FileIDs: []string{change.ID}}}}})
	if err != nil || len(result.Hashes) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := gitExec(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "new.txt\nold.txt\n" {
		t.Fatalf("rename paths = %q", got)
	}
	if got := gitExec(t, repo, "status", "--short"); got != "" {
		t.Fatalf("rename left changes: %q", got)
	}
}

func TestExecuteAbsorbsTargetPartialStageAndPreservesOutsideStage(t *testing.T) {
	repo := newRepo(t, "a.txt", "outside.txt")
	writeFile(t, repo, "a.txt", "staged version\n")
	gitExec(t, repo, "add", "a.txt")
	writeFile(t, repo, "a.txt", "working tree final\n")
	writeFile(t, repo, "outside.txt", "outside staged\n")
	gitExec(t, repo, "add", "outside.txt")
	snapshot := collect(t, repo)
	byPath := changesByPath(snapshot)

	_, err := Execute(Options{Root: repo, Changes: []gitstate.Change{byPath["a.txt"]}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{byPath["a.txt"].ID}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitExec(t, repo, "show", "HEAD:a.txt"); got != "working tree final\n" {
		t.Fatalf("committed content = %q", got)
	}
	if got := gitExec(t, repo, "diff", "--cached", "--name-only"); got != "outside.txt\n" {
		t.Fatalf("outside index = %q", got)
	}
}

func TestExecuteCancellationRestoresIndexWithoutCommit(t *testing.T) {
	repo := newRepo(t, "a.txt", "outside.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	writeFile(t, repo, "outside.txt", "outside1\n")
	gitExec(t, repo, "add", "outside.txt")
	snapshot := collect(t, repo)
	byPath := changesByPath(snapshot)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Execute(Options{Context: ctx, Root: repo, Changes: []gitstate.Change{byPath["a.txt"]}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{byPath["a.txt"].ID}}}}})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != ExitInterrupted || !failure.Restored {
		t.Fatalf("err=%#v", err)
	}
	if got := gitExec(t, repo, "diff", "--cached", "--name-only"); got != "outside.txt\n" {
		t.Fatalf("restored index = %q", got)
	}
	if got := gitExec(t, repo, "log", "--format=%s", "-1"); got != "base\n" {
		t.Fatalf("unexpected commit = %q", got)
	}
}

func TestExecuteReportsUnknownRestorationState(t *testing.T) {
	repo := newRepo(t, "a.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	change := collect(t, repo).Changes[0]
	previous := restoreInitialIndex
	restoreInitialIndex = func(string, []byte) error { return errors.New("fixture restoration failure") }
	t.Cleanup(func() { restoreInitialIndex = previous })

	_, err := Execute(Options{Root: repo, Changes: []gitstate.Change{change}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "invalid empty assignment"}}}})
	var failure *Error
	if !errors.As(err, &failure) || failure.Restored || failure.Message != "commit failed and index restoration is unknown" {
		t.Fatalf("err=%#v", err)
	}
}

func TestExecuteEscapesHookOutput(t *testing.T) {
	repo := newRepo(t, "a.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	snapshot := collect(t, repo)
	change := snapshot.Changes[0]
	writeHook(t, repo, "pre-commit", "#!/bin/sh\nprintf 'forged\\n\\033[31mred\\r' >&2\nexit 1\n")

	_, err := Execute(Options{Root: repo, Changes: []gitstate.Change{change}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{change.ID}}}}})
	var failure *Error
	if !errors.As(err, &failure) || strings.ContainsAny(failure.HookOutput, "\n\r\x1b") || !strings.Contains(failure.HookOutput, `\n`) || !failure.Restored {
		t.Fatalf("hook output/error = %#v", failure)
	}
}

func TestExecutePreservesCommitWhenHashLookupFails(t *testing.T) {
	repo := newRepo(t, "a.txt", "outside.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	writeFile(t, repo, "outside.txt", "outside1\n")
	gitExec(t, repo, "add", "outside.txt")
	byPath := changesByPath(collect(t, repo))
	previous := readHEAD
	calls := 0
	readHEAD = func(root string) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("fixture hash lookup failure")
		}
		return gitText(root, "rev-parse", "HEAD")
	}
	t.Cleanup(func() { readHEAD = previous })

	result, err := Execute(Options{Root: repo, Changes: []gitstate.Change{byPath["a.txt"]}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{byPath["a.txt"].ID}}}}})
	var failure *Error
	if !errors.As(err, &failure) || failure.Message != "commit was created but its hash could not be determined" || !failure.Restored || len(result.Hashes) != 0 {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
	if got := gitExec(t, repo, "log", "-1", "--format=%s"); got != "fix(a): update a\n" {
		t.Fatalf("latest commit = %q", got)
	}
	if got := gitExec(t, repo, "diff", "--cached", "--name-only"); got != "outside.txt\n" {
		t.Fatalf("restored index = %q", got)
	}
}

func TestExecuteWritesEscapedSuccessfulHookOutput(t *testing.T) {
	repo := newRepo(t, "a.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	change := collect(t, repo).Changes[0]
	writeHook(t, repo, "pre-commit", "#!/bin/sh\nprintf 'verified\\n\\033[31mgreen\\r' >&2\n")
	var writer bytes.Buffer

	_, err := Execute(Options{Root: repo, Changes: []gitstate.Change{change}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{change.ID}}}}, Writer: &writer})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(writer.String(), "\n\r\x1b") || !strings.Contains(writer.String(), `verified\n\x1b[31mgreen\r`) {
		t.Fatalf("unsafe successful hook output: %q", writer.String())
	}
}

func TestExecuteValidatesCommitCreatedByFailingHook(t *testing.T) {
	repo := newRepo(t, "a.txt", "outside.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	writeFile(t, repo, "outside.txt", "outside1\n")
	change := changesByPath(collect(t, repo))["a.txt"]
	writeHook(t, repo, "pre-commit", "#!/bin/sh\ngit add outside.txt\ngit commit --no-verify -m hook-created >/dev/null\nexit 1\n")

	result, err := Execute(Options{Root: repo, Changes: []gitstate.Change{change}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{change.ID}}}}})
	var failure *Error
	if !errors.As(err, &failure) || failure.Message != "commit file set does not match its assignment" || !reflect.DeepEqual(failure.Paths, []string{"outside.txt"}) || len(result.Hashes) != 1 || failure.CommitHash != result.Hashes[0] || !failure.Restored {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
	if got := gitExec(t, repo, "log", "-1", "--format=%s"); got != "hook-created\n" {
		t.Fatalf("created commit was rolled back: %q", got)
	}
}

func TestExecuteReportsHashWhenHookOutputWriterFails(t *testing.T) {
	repo := newRepo(t, "a.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	change := collect(t, repo).Changes[0]

	result, err := Execute(Options{Root: repo, Changes: []gitstate.Change{change}, Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{change.ID}}}}, Writer: failingWriter{}})
	var failure *Error
	if !errors.As(err, &failure) || failure.Message != "commit was created but hook output could not be written" || len(result.Hashes) != 1 || failure.CommitHash != result.Hashes[0] || !failure.Restored {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
	if got := gitExec(t, repo, "diff-tree", "--no-commit-id", "--name-only", "-r", result.Hashes[0]); got != "a.txt\n" {
		t.Fatalf("created commit file set = %q", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture writer failure")
}

func TestSymmetricDifferenceReportsExtraAndMissingPaths(t *testing.T) {
	got := symmetricDifference(map[string]bool{"extra": true}, map[string]bool{"missing": true})
	if !reflect.DeepEqual(got, []string{"extra", "missing"}) {
		t.Fatalf("difference = %#v", got)
	}
}

func newRepo(t *testing.T, paths ...string) string {
	t.Helper()
	repo := t.TempDir()
	gitExec(t, repo, "init", "-b", "main")
	gitExec(t, repo, "config", "user.email", "test@example.invalid")
	gitExec(t, repo, "config", "user.name", "Test User")
	for _, path := range paths {
		writeFile(t, repo, path, path+" base\n")
	}
	gitExec(t, repo, "add", ".")
	gitExec(t, repo, "commit", "-m", "base")
	return repo
}

func collect(t *testing.T, repo string) gitstate.Snapshot {
	t.Helper()
	snapshot, err := gitstate.Collect(repo, gitstate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func changesByPath(snapshot gitstate.Snapshot) map[string]gitstate.Change {
	result := map[string]gitstate.Change{}
	for _, change := range snapshot.Changes {
		if change.NewPath != nil {
			result[*change.NewPath] = change
		}
	}
	return result
}

func writeHook(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
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
