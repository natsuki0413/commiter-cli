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
	result, err := Run(context.Background(), root, definition, 5*time.Second, StatePolicy{})
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
	result, err := Run(context.Background(), verificationRepository(t), definition, time.Second, StatePolicy{})
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

	result, err := Run(context.Background(), root, definition, time.Second, StatePolicy{})
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
	_, err := Run(context.Background(), root, definition, 10*time.Millisecond, StatePolicy{})
	var failure *RunError
	if !errors.As(err, &failure) || failure.Kind != RunTimedOut {
		t.Fatalf("timeout error = %#v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Run(ctx, root, definition, time.Second, StatePolicy{})
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
	before, err := CaptureRepositoryState(repo, StatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	writeVerificationFile(t, repo, "tracked.txt", "after mutation\n")
	gitVerification(t, repo, "add", "tracked.txt")
	after, err := CaptureRepositoryState(repo, StatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, after); !reflect.DeepEqual(got, []string{"tracked.txt"}) {
		t.Fatalf("changed paths = %#v", got)
	}
	if err := before.RestoreIndex(); err != nil {
		t.Fatal(err)
	}
	restored, err := CaptureRepositoryState(repo, StatePolicy{})
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
	before, err := CaptureRepositoryState(repo, StatePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	gitVerification(t, repo, "add", "tracked.txt")
	writeVerificationFile(t, repo, "untracked.txt", "new\n")
	after, err := CaptureRepositoryState(repo, StatePolicy{TargetUntracked: []string{"untracked.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, after); !reflect.DeepEqual(got, []string{"tracked.txt", "untracked.txt"}) {
		t.Fatalf("changed paths = %#v", got)
	}
}

func TestRepositoryStateIgnoresUnselectedUntrackedFiles(t *testing.T) {
	repo := verificationRepository(t)
	before, err := CaptureRepositoryState(repo, StatePolicy{TargetUntracked: []string{"target.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	writeVerificationFile(t, repo, "coverage.out", "not selected\n")
	afterOutside, err := CaptureRepositoryState(repo, StatePolicy{TargetUntracked: []string{"target.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, afterOutside); len(got) != 0 {
		t.Fatalf("unselected untracked file was treated as mutation: %#v", got)
	}

	writeVerificationFile(t, repo, "target.txt", "selected\n")
	afterTarget, err := CaptureRepositoryState(repo, StatePolicy{TargetUntracked: []string{"target.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(afterOutside, afterTarget); !reflect.DeepEqual(got, []string{"target.txt"}) {
		t.Fatalf("selected untracked mutation = %#v", got)
	}
}

func TestRepositoryStateHashesDirtyTrackedContentIndependentlyOfMetadata(t *testing.T) {
	repo := verificationRepository(t)
	writeVerificationFile(t, repo, "tracked.txt", "base\n")
	gitVerification(t, repo, "add", "tracked.txt")
	gitVerification(t, repo, "commit", "-m", "base")
	writeVerificationFile(t, repo, "tracked.txt", "aaaa\n")
	path := filepath.Join(repo, "tracked.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := CaptureRepositoryState(repo, StatePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	writeVerificationFile(t, repo, "tracked.txt", "bbbb\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := CaptureRepositoryState(repo, StatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, after); !reflect.DeepEqual(got, []string{"tracked.txt"}) {
		t.Fatalf("same-size same-mtime content mutation = %#v", got)
	}
}

func TestRepositoryStateDoesNotOpenExcludedOrUnapprovedSensitiveContent(t *testing.T) {
	repo := verificationRepository(t)
	for _, name := range []string{".env", "credentials.json", "private.cfg", "safe.txt"} {
		writeVerificationFile(t, repo, name, "base\n")
	}
	gitVerification(t, repo, "add", ".env", "credentials.json", "private.cfg", "safe.txt")
	gitVerification(t, repo, "commit", "-m", "base")
	for _, name := range []string{".env", "credentials.json", "private.cfg", "safe.txt"} {
		writeVerificationFile(t, repo, name, "changed\n")
	}

	previousOpen := openContent
	opened := []string{}
	openContent = func(path string) (*os.File, error) {
		opened = append(opened, filepath.Base(path))
		return os.Open(path)
	}
	t.Cleanup(func() { openContent = previousOpen })

	state, err := CaptureRepositoryState(repo, StatePolicy{
		ApprovedSensitive:        []string{".env", "private.cfg"},
		AdditionalSensitiveGlobs: []string{"private.cfg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opened, []string{"safe.txt"}) {
		t.Fatalf("opened files = %#v, want only safe.txt", opened)
	}
	for _, name := range []string{".env", "credentials.json", "private.cfg"} {
		if identity := state.Files[name].ContentIdentity; identity != "" {
			t.Fatalf("sensitive content identity for %s = %q", name, identity)
		}
	}
}

func TestRepositoryStateHashesApprovedSensitiveCandidate(t *testing.T) {
	repo := verificationRepository(t)
	writeVerificationFile(t, repo, "credentials.json", "base\n")
	gitVerification(t, repo, "add", "credentials.json")
	gitVerification(t, repo, "commit", "-m", "base")
	writeVerificationFile(t, repo, "credentials.json", "aaaa\n")
	path := filepath.Join(repo, "credentials.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := StatePolicy{ApprovedSensitive: []string{"credentials.json"}}
	before, err := CaptureRepositoryState(repo, policy)
	if err != nil {
		t.Fatal(err)
	}

	writeVerificationFile(t, repo, "credentials.json", "bbbb\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := CaptureRepositoryState(repo, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := ChangedPaths(before, after); !reflect.DeepEqual(got, []string{"credentials.json"}) {
		t.Fatalf("approved sensitive content mutation = %#v", got)
	}
}

func TestRepositoryStateAppliesSensitivePolicyToWholeRename(t *testing.T) {
	tests := []struct {
		name       string
		oldPath    string
		policy     StatePolicy
		wantOpened []string
	}{
		{name: "unapproved candidate", oldPath: "credentials.json"},
		{name: "automatic exclusion", oldPath: ".env", policy: StatePolicy{ApprovedSensitive: []string{".env", "safe.txt"}}},
		{name: "additional exclusion", oldPath: "private.cfg", policy: StatePolicy{ApprovedSensitive: []string{"private.cfg", "safe.txt"}, AdditionalSensitiveGlobs: []string{"private.cfg"}}},
		{name: "approved candidate", oldPath: "credentials.json", policy: StatePolicy{ApprovedSensitive: []string{"credentials.json", "safe.txt"}}, wantOpened: []string{"safe.txt"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := verificationRepository(t)
			writeVerificationFile(t, repo, test.oldPath, "fixture\n")
			gitVerification(t, repo, "add", test.oldPath)
			gitVerification(t, repo, "commit", "-m", "base")
			gitVerification(t, repo, "mv", test.oldPath, "safe.txt")

			previousOpen := openContent
			var opened []string
			openContent = func(path string) (*os.File, error) {
				opened = append(opened, filepath.Base(path))
				return os.Open(path)
			}
			t.Cleanup(func() { openContent = previousOpen })

			state, err := CaptureRepositoryState(repo, test.policy)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(opened, test.wantOpened) {
				t.Fatalf("opened files = %#v, want %#v", opened, test.wantOpened)
			}
			if (state.Files["safe.txt"].ContentIdentity != "") != (len(test.wantOpened) > 0) {
				t.Fatalf("safe.txt content identity = %q", state.Files["safe.txt"].ContentIdentity)
			}
		})
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
