package cli

import (
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
)

func TestAnalyzeForPlanningExtractsValuesOnlyFromApprovedSensitiveCandidates(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "normal.txt", "base\n", 0o644)
	cliWrite(t, repo, "auth.json", "{}\n", 0o600)
	cliGit(t, repo, "add", "normal.txt", "auth.json")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "normal.txt", "token = false\n", 0o644)
	cliWrite(t, repo, "auth.json", `{"token":"approved-fixture-value"}`+"\n", 0o600)
	snapshot, err := gitstate.Collect(repo, gitstate.Options{
		ApproveSensitiveCandidates: func([]gitstate.Candidate) (bool, error) {
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, sensitive, err := analyzeForPlanning(repo, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if sensitive.Contains([]byte("false")) {
		t.Fatal("normal file value was treated as an approved sensitive value")
	}
	if !sensitive.Contains([]byte("approved-fixture-value")) {
		t.Fatal("approved sensitive candidate value was not extracted")
	}
}

func TestWorktreeDiffTreatsCollectedPathsLiterally(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "*", "literal base\n", 0o644)
	cliWrite(t, repo, "outside.txt", "outside base\n", 0o644)
	cliGit(t, repo, "add", "*", "outside.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "*", "literal change\n", 0o644)
	cliWrite(t, repo, "outside.txt", "outside change\n", 0o644)

	diff, err := worktreeDiff(repo, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "literal change") || strings.Contains(diff, "outside change") {
		t.Fatalf("worktree diff expanded collected path as pathspec: %q", diff)
	}
}
