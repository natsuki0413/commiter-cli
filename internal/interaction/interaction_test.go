package interaction

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/planning"
)

func TestReviewApproveRegenerateAndReject(t *testing.T) {
	plan := planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "cli", Summary: "safe output", FileIDs: []string{"F001"}}}}
	for _, test := range []struct {
		name, input string
		decision    Decision
		supplement  string
	}{
		{"approve", "y\n", Approve, ""},
		{"regenerate", "r\nmake one commit\n", Regenerate, "make one commit"},
		{"reject", "n\n", Reject, ""},
		{"eof", "", Reject, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			decision, supplement, err := (Reviewer{In: strings.NewReader(test.input), Printer: output.New(&out, &stderr, false)}).Review(ReviewRequest{Plan: plan, Files: map[string]string{"F001": "main.go"}})
			if err != nil || decision != test.decision || supplement != test.supplement {
				t.Fatalf("decision=%q supplement=%q err=%v", decision, supplement, err)
			}
			if strings.Contains(out.String(), "secret") {
				t.Fatal("review output contains unexpected sensitive text")
			}
		})
	}
}

func TestRenderEscapesUntrustedPlanAndMetadata(t *testing.T) {
	request := ReviewRequest{
		Plan:         planning.Plan{SchemaVersion: planning.SchemaVersion, Commits: []planning.Commit{{Type: "fix", Scope: "cli", Summary: "forged\x1b]0;title\a\nline", FileIDs: []string{"F001"}}}},
		Files:        map[string]string{"F001": "path\nforged"},
		Excluded:     []Excluded{{Path: "secret\rname", Reason: "reason\tvalue"}},
		Verification: []VerificationCommand{{Name: "test\nforged", CWD: ".", Argv: []string{"go", "test\nforged"}}},
		PushTarget:   PushTarget{Resolved: true, Remote: "origin", Branch: "main\nforged"},
	}
	var stdout, stderr bytes.Buffer
	if err := Render(output.New(&stdout, &stderr, false), request); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stdout.String(), "\r\x1b\a\t") || strings.Count(stdout.String(), "\n") != 5 {
		t.Fatalf("unsafe render output: %q", stdout.String())
	}
	for _, escaped := range []string{`\x1b`, `\a`, `\n`, `\r`, `\t`} {
		if !strings.Contains(stdout.String(), escaped) {
			t.Fatalf("missing escape %q in %q", escaped, stdout.String())
		}
	}
}

func TestResolvePushTargetUsesUpstreamAndFallsBackToSingleRemote(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "remote", "add", "origin", "https://example.invalid/repo.git")
	got := ResolvePushTarget(repo)
	if !got.Resolved || got.Remote != "origin" || got.Branch != "main" {
		t.Fatalf("single remote target=%#v", got)
	}
	runGit(t, repo, "remote", "add", "upstream", "https://example.invalid/upstream.git")
	runGit(t, repo, "config", "branch.main.remote", "upstream")
	runGit(t, repo, "config", "branch.main.merge", "refs/heads/release")
	got = ResolvePushTarget(repo)
	if !got.Resolved || got.Remote != "upstream" || got.Branch != "release" {
		t.Fatalf("upstream target=%#v", got)
	}
}

func TestResolvePushTargetRejectsInvalidRefAndUnknownUpstream(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "remote", "add", "origin", "https://example.invalid/repo.git")
	runGit(t, repo, "config", "branch.main.remote", "missing")
	runGit(t, repo, "config", "branch.main.merge", "refs/heads/a..b")
	got := ResolvePushTarget(repo)
	if !got.Resolved || got.Remote != "origin" || got.Branch != "main" {
		t.Fatalf("invalid upstream did not use safe fallback: %#v", got)
	}
	runGit(t, repo, "remote", "add", "two", "https://example.invalid/two.git")
	if got := ResolvePushTarget(repo); got.Resolved {
		t.Fatalf("invalid upstream resolved with ambiguous remotes: %#v", got)
	}
}

func TestResolvePushTargetRejectsAmbiguousAndDetached(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "remote", "add", "one", "https://example.invalid/one.git")
	runGit(t, repo, "remote", "add", "two", "https://example.invalid/two.git")
	if got := ResolvePushTarget(repo); got.Resolved {
		t.Fatalf("ambiguous target resolved: %#v", got)
	}
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "commit", "--allow-empty", "-m", "base")
	runGit(t, repo, "checkout", "--detach")
	if got := ResolvePushTarget(repo); got.Resolved {
		t.Fatalf("detached HEAD resolved: %#v", got)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", repo}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
