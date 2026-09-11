package verification

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestRunExecutesArgvSequentiallyWithoutShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX printf fixture")
	}
	root := verificationRepository(t)
	definition := &Definition{Commands: []Command{
		{Name: "first", CWD: ".", Argv: []string{"printf", "%s", "one; printf injected"}},
		{Name: "second", CWD: ".", Argv: []string{"printf", "%s", "two"}},
	}}
	result, err := Run(context.Background(), root, definition, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []CommandResult{{Name: "first", Output: "one; printf injected"}, {Name: "second", Output: "two"}}
	if !reflect.DeepEqual(result.Commands, want) {
		t.Fatalf("result = %#v, want %#v", result.Commands, want)
	}
}

func TestRunStopsAfterFailureAndKeepsOutput(t *testing.T) {
	definition := &Definition{Commands: []Command{
		{Name: "failure", CWD: ".", Argv: []string{"sh", "-c", "printf failed; exit 9"}},
		{Name: "unreached", CWD: ".", Argv: []string{"sh", "-c", "printf reached"}},
	}}
	result, err := Run(context.Background(), verificationRepository(t), definition, time.Second)
	var failure *RunError
	if !errors.As(err, &failure) || failure.Kind != RunFailed || failure.Command != "failure" {
		t.Fatalf("error = %#v", err)
	}
	if len(result.Commands) != 1 || result.Commands[0].Output != "failed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunAttributesGitVisibleMutationToCommand(t *testing.T) {
	root := verificationRepository(t)
	writeVerificationFile(t, root, "tracked.txt", "base\n")
	gitVerification(t, root, "add", "tracked.txt")
	gitVerification(t, root, "commit", "-m", "base")
	definition := &Definition{Commands: []Command{{Name: "mutator", CWD: ".", Argv: []string{"sh", "-c", "printf changed > tracked.txt"}}}}

	result, err := Run(context.Background(), root, definition, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Commands) != 1 || !reflect.DeepEqual(result.Commands[0].ChangedPaths, []string{"tracked.txt"}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunClassifiesTimeoutAndInterruption(t *testing.T) {
	definition := &Definition{Commands: []Command{{Name: "wait", CWD: ".", Argv: []string{"sh", "-c", "sleep 5"}}}}
	root := verificationRepository(t)
	_, err := Run(context.Background(), root, definition, 10*time.Millisecond)
	var failure *RunError
	if !errors.As(err, &failure) || failure.Kind != RunTimedOut {
		t.Fatalf("timeout error = %#v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Run(ctx, root, definition, time.Second)
	if !errors.As(err, &failure) || failure.Kind != RunInterrupted {
		t.Fatalf("interruption error = %#v", err)
	}
}

func TestRepositoryStateDetectsWorktreeAndRestoresIndex(t *testing.T) {
	repo := verificationRepository(t)
	writeVerificationFile(t, repo, "tracked.txt", "base\n")
	gitVerification(t, repo, "add", "tracked.txt")
	gitVerification(t, repo, "commit", "-m", "base")
	writeVerificationFile(t, repo, "tracked.txt", "before\n")
	before, err := CaptureRepositoryState(repo)
	if err != nil {
		t.Fatal(err)
	}
	writeVerificationFile(t, repo, "tracked.txt", "after mutation\n")
	gitVerification(t, repo, "add", "tracked.txt")
	after, err := CaptureRepositoryState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, after); !reflect.DeepEqual(got, []string{"tracked.txt"}) {
		t.Fatalf("changed paths = %#v", got)
	}
	if err := before.RestoreIndex(); err != nil {
		t.Fatal(err)
	}
	restored, err := CaptureRepositoryState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.IndexData, before.IndexData) {
		t.Fatal("index was not restored")
	}
}

func TestRepositoryStateDetectsUntrackedAndIndexOnlyMutations(t *testing.T) {
	repo := verificationRepository(t)
	writeVerificationFile(t, repo, "tracked.txt", "base\n")
	gitVerification(t, repo, "add", "tracked.txt")
	gitVerification(t, repo, "commit", "-m", "base")
	writeVerificationFile(t, repo, "tracked.txt", "changed\n")
	before, err := CaptureRepositoryState(repo)
	if err != nil {
		t.Fatal(err)
	}

	gitVerification(t, repo, "add", "tracked.txt")
	writeVerificationFile(t, repo, "untracked.txt", "new\n")
	after, err := CaptureRepositoryState(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, after); !reflect.DeepEqual(got, []string{"tracked.txt", "untracked.txt"}) {
		t.Fatalf("changed paths = %#v", got)
	}
}

func verificationRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitVerification(t, repo, "init", "-b", "main")
	gitVerification(t, repo, "config", "user.name", "Test User")
	gitVerification(t, repo, "config", "user.email", "test@example.invalid")
	gitVerification(t, repo, "commit", "--allow-empty", "-m", "initial")
	return repo
}

func gitVerification(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func writeVerificationFile(t *testing.T, repo, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
