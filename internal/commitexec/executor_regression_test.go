package commitexec

import (
	"context"
	"errors"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/planning"
)

func TestExecuteAllowsFormatterHookToModifyCurrentAssignment(t *testing.T) {
	repo := newRepo(t, "a.txt")
	writeFile(t, repo, "a.txt", "unformatted\n")
	change := changesByPath(collect(t, repo))["a.txt"]
	writeHook(t, repo, "pre-commit", "#!/bin/sh\nprintf 'formatted\\n' > a.txt\ngit add a.txt\n")

	result, err := Execute(Options{
		Root:    repo,
		Changes: []gitstate.Change{change},
		Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{
			{Type: "fix", Scope: "a", Summary: "format a", FileIDs: []string{change.ID}},
		}},
	})
	if err != nil || len(result.Hashes) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if got := gitExec(t, repo, "show", "HEAD:a.txt"); got != "formatted\n" {
		t.Fatalf("committed formatted content = %q", got)
	}
	if got := gitExec(t, repo, "status", "--short"); got != "" {
		t.Fatalf("formatter hook left changes: %q", got)
	}
}

func TestExecuteCancellationAfterCommitKeepsCommitAndRestoresOutsideIndex(t *testing.T) {
	repo := newRepo(t, "a.txt", "b.txt", "outside.txt")
	writeFile(t, repo, "a.txt", "a1\n")
	writeFile(t, repo, "b.txt", "b1\n")
	writeFile(t, repo, "outside.txt", "outside1\n")
	gitExec(t, repo, "add", "outside.txt")
	byPath := changesByPath(collect(t, repo))
	writeHook(t, repo, "pre-commit", "#!/bin/sh\nprintf 'checked\\n' >&2\n")
	ctx, cancel := context.WithCancel(context.Background())

	result, err := Execute(Options{
		Context: ctx,
		Root:    repo,
		Changes: []gitstate.Change{byPath["a.txt"], byPath["b.txt"]},
		Plan: planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{
			{Type: "fix", Scope: "a", Summary: "update a", FileIDs: []string{byPath["a.txt"].ID}},
			{Type: "fix", Scope: "b", Summary: "update b", FileIDs: []string{byPath["b.txt"].ID}},
		}},
		Writer: cancelWriter{cancel: cancel},
	})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != ExitInterrupted || !failure.Restored || len(result.Hashes) != 1 {
		t.Fatalf("result=%#v err=%#v", result, err)
	}
	if got := gitExec(t, repo, "log", "--format=%s", "-2"); got != "fix(a): update a\nbase\n" {
		t.Fatalf("unexpected commits after cancellation: %q", got)
	}
	if got := gitExec(t, repo, "diff", "--cached", "--name-only"); got != "outside.txt\n" {
		t.Fatalf("restored index = %q", got)
	}
	if got := gitExec(t, repo, "diff", "--name-only"); got != "b.txt\n" {
		t.Fatalf("uncommitted work after cancellation = %q", got)
	}
}

type cancelWriter struct {
	cancel context.CancelFunc
}

func (w cancelWriter) Write(p []byte) (int, error) {
	w.cancel()
	return len(p), nil
}
