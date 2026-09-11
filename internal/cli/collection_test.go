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
	"time"

	"github.com/natsuki0413/commiter-cli/internal/commitexec"
	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/planning"
	"github.com/natsuki0413/commiter-cli/internal/verification"
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
	oldInput := mainInput
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })
	beforeHead := cliGitOutput(t, repo, "rev-parse", "HEAD")
	beforeIndex := cliGitOutput(t, repo, "diff", "--cached", "--binary")
	beforeConfig := cliGitOutput(t, repo, "config", "--local", "--null", "--list")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "--dry-run"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "Read all listed candidates?") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "this-value") {
		t.Fatalf("sensitive value was printed: stdout=%q stderr=%q", stdout.String(), stderr.String())
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
	if len(result.Snapshot.Changes) != 3 || len(result.Snapshot.Excluded) != 1 || len(result.Plan.Commits) != 1 || !result.DryRun || result.Verification.Executed {
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

func TestMainDoesNotApproveSensitiveCandidatesAtEOF(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "README.md", "base\n", 0o644)
	cliGit(t, repo, "add", "README.md")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "auth.json", "local-only\n", 0o600)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	oldInput := mainInput
	mainInput = strings.NewReader("y")
	t.Cleanup(func() { mainInput = oldInput })

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "No target changes.") || strings.Contains(stdout.String(), "F001") || strings.Contains(stdout.String(), "local-only") {
		t.Fatalf("sensitive candidate was approved at EOF: %q", stdout.String())
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

func TestMainRunsVerificationBeforeCommit(t *testing.T) {
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
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })

	order := []string{}
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			order = append(order, "verification")
			return verification.RunResult{}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			order = append(order, "commit")
			return commitexec.Result{Hashes: []string{"fixture-hash"}}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Join(order, ",") != "verification,commit" || !strings.Contains(stdout.String(), "fixture-hash") {
		t.Fatalf("order=%#v stdout=%q", order, stdout.String())
	}
}

func TestMainStopsOnVerificationMutationAndRestoresInitialIndex(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliGit(t, repo, "add", "a.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\nn\n")
	t.Cleanup(func() { mainInput = oldInput })
	commitCalled := false
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			cliWrite(t, repo, "a.txt", "verification mutation\n", 0o644)
			cliGit(t, repo, "add", "a.txt")
			return verification.RunResult{Commands: []verification.CommandResult{{Name: "fixture"}}}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			commitCalled = true
			return commitexec.Result{}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != exitcode.Safety || commitCalled {
		t.Fatalf("code=%d commitCalled=%t stdout=%q stderr=%q", code, commitCalled, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "a.txt") || !strings.Contains(stdout.String(), "Re-analyze changed state?") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if cached := cliGitOutput(t, repo, "diff", "--cached", "--name-only"); cached != "" {
		t.Fatalf("index was not restored: %q", cached)
	}
	contents, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	if err != nil || string(contents) != "verification mutation\n" {
		t.Fatalf("working tree was rolled back: %q, %v", contents, err)
	}
}

func TestMainReanalyzesCurrentStateAfterMutationApproval(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliGit(t, repo, "add", "a.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	plans := []string{}
	stubPlanFlow(t, &plans)
	oldInput := mainInput
	mainInput = strings.NewReader("y\ny\ny\n")
	t.Cleanup(func() { mainInput = oldInput })
	runs := 0
	commits := 0
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			runs++
			if runs == 1 {
				cliWrite(t, repo, "a.txt", "reanalyzed\n", 0o644)
			}
			return verification.RunResult{Commands: []verification.CommandResult{{Name: "fixture"}}}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			commits++
			return commitexec.Result{Hashes: []string{"fixture-hash"}}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if runs != 2 || commits != 1 || len(plans) != 2 {
		t.Fatalf("runs=%d commits=%d plans=%#v", runs, commits, plans)
	}
}

func TestMainMapsVerificationFailureAndEscapesOutput(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliGit(t, repo, "add", "a.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			return verification.RunResult{Commands: []verification.CommandResult{{Name: "fixture", Output: "bad\x1b[2J\nforged"}}}, &verification.RunError{Kind: verification.RunFailed, Command: "fixture"}
		},
		func(commitexec.Options) (commitexec.Result, error) {
			t.Fatal("commit must not run after verification failure")
			return commitexec.Result{}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != exitcode.Verification {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.ContainsRune(stdout.String(), '\x1b') || !strings.Contains(stdout.String(), `\x1b`) || !strings.Contains(stdout.String(), `\n`) {
		t.Fatalf("verification output was not escaped: %q", stdout.String())
	}
}

func TestMainAllowsIgnoredVerificationOutput(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, ".gitignore", "generated/\n", 0o644)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliGit(t, repo, "add", ".gitignore", "a.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })
	commitCalled := false
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			cliWrite(t, repo, "generated/output.txt", "ignored\n", 0o644)
			return verification.RunResult{}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			commitCalled = true
			return commitexec.Result{Hashes: []string{"fixture-hash"}}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 || !commitCalled || stderr.Len() != 0 {
		t.Fatalf("code=%d commitCalled=%t stdout=%q stderr=%q", code, commitCalled, stdout.String(), stderr.String())
	}
}

func TestMainAllowsUnselectedUntrackedVerificationOutput(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliGit(t, repo, "add", "a.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\n")
	t.Cleanup(func() { mainInput = oldInput })
	commitCalled := false
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			cliWrite(t, repo, "coverage.out", "outside selected pathspec\n", 0o644)
			return verification.RunResult{}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			commitCalled = true
			return commitexec.Result{Hashes: []string{"fixture-hash"}}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"a.txt"}, &stdout, &stderr); code != 0 || !commitCalled || stderr.Len() != 0 {
		t.Fatalf("code=%d commitCalled=%t stdout=%q stderr=%q", code, commitCalled, stdout.String(), stderr.String())
	}
}

func TestMainStopsForNewUntrackedInsideSelection(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliGit(t, repo, "add", "a.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\nn\n")
	t.Cleanup(func() { mainInput = oldInput })
	commitCalled := false
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			cliWrite(t, repo, "new-target.txt", "new target\n", 0o644)
			return verification.RunResult{}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			commitCalled = true
			return commitexec.Result{}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != exitcode.Safety || commitCalled {
		t.Fatalf("code=%d commitCalled=%t stdout=%q stderr=%q", code, commitCalled, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "new-target.txt") || !strings.Contains(stdout.String(), "Re-analyze changed state?") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestMainDetectsUnselectedDirtyTrackedContentWithStableMetadata(t *testing.T) {
	repo := cliRepository(t)
	cliWrite(t, repo, "a.txt", "base\n", 0o644)
	cliWrite(t, repo, "outside.txt", "base\n", 0o644)
	cliGit(t, repo, "add", "a.txt", "outside.txt")
	cliGit(t, repo, "commit", "-m", "base")
	cliWrite(t, repo, "a.txt", "planned\n", 0o644)
	cliWrite(t, repo, "outside.txt", "aaaa\n", 0o644)
	outsidePath := filepath.Join(repo, "outside.txt")
	chdir(t, repo)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubPlanFlow(t, nil)
	oldInput := mainInput
	mainInput = strings.NewReader("y\nn\n")
	t.Cleanup(func() { mainInput = oldInput })
	commitCalled := false
	stubPostApprovalFlows(t,
		func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error) {
			info, err := os.Stat(outsidePath)
			if err != nil {
				t.Fatal(err)
			}
			cliWrite(t, repo, "outside.txt", "bbbb\n", 0o644)
			if err := os.Chtimes(outsidePath, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			return verification.RunResult{}, nil
		},
		func(commitexec.Options) (commitexec.Result, error) {
			commitCalled = true
			return commitexec.Result{}, nil
		},
	)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"a.txt"}, &stdout, &stderr); code != exitcode.Safety || commitCalled {
		t.Fatalf("code=%d commitCalled=%t stdout=%q stderr=%q", code, commitCalled, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "outside.txt") || !strings.Contains(stdout.String(), "Re-analyze changed state?") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func stubPostApprovalFlows(t *testing.T, verify func(context.Context, string, *verification.Definition, time.Duration, []string) (verification.RunResult, error), commit func(commitexec.Options) (commitexec.Result, error)) {
	t.Helper()
	previousVerification, previousCommit := verificationFlow, commitFlow
	verificationFlow, commitFlow = verify, commit
	t.Cleanup(func() {
		verificationFlow, commitFlow = previousVerification, previousCommit
	})
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
