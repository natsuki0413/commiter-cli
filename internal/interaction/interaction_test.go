package interaction

import (
	"os/exec"
	"strings"
	"testing"

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
			var out strings.Builder
			decision, supplement, err := (Reviewer{In: strings.NewReader(test.input), Out: &out}).Review(ReviewRequest{Plan: plan})
			if err != nil || decision != test.decision || supplement != test.supplement {
				t.Fatalf("decision=%q supplement=%q err=%v", decision, supplement, err)
			}
			if strings.Contains(out.String(), "secret") {
				t.Fatal("review output contains unexpected sensitive text")
			}
		})
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
	runGit(t, repo, "config", "branch.main.remote", "upstream")
	runGit(t, repo, "config", "branch.main.merge", "refs/heads/release")
	got = ResolvePushTarget(repo)
	if !got.Resolved || got.Remote != "upstream" || got.Branch != "release" {
		t.Fatalf("upstream target=%#v", got)
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
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", repo}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
