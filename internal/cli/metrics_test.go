package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/natsuki0413/commiter-cli/internal/commitexec"
	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/interaction"
	runmetrics "github.com/natsuki0413/commiter-cli/internal/metrics"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/planning"
	"github.com/natsuki0413/commiter-cli/internal/verification"
)

func TestFinishMetricsDisplaysWithoutPersistingByDefault(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	recorder := runmetrics.New()
	recorder.AddDuration(runmetrics.GitPreprocessing, time.Nanosecond)
	recorder.SetPlanning("model:tag", "8k", 1, 2, 3, 1, 0, 0)
	var stdout, stderr bytes.Buffer

	code := finishMetrics(output.New(&stdout, &stderr, false), recorder, stateDir, false, exitcode.Safety)
	if code != exitcode.Safety {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "metrics.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("metrics file unexpectedly exists: %v", err)
	}
	value := stdout.String()
	for _, want := range []string{"git preprocessing:", "files=1", "exit: safety_stop"} {
		if !strings.Contains(value, want) {
			t.Fatalf("output=%q missing %q", value, want)
		}
	}
	if strings.Contains(value, "push:") {
		t.Fatalf("unexecuted push phase was displayed: %q", value)
	}
}

func TestFinishMetricsPersistsOnlyFixedSchemaAndKeepsJSONStdoutClean(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	recorder := runmetrics.New()
	recorder.AddDuration(runmetrics.Generation, time.Nanosecond)
	recorder.SetPlanning("model:tag", "16k", 2, 4, 8, 1, 1, 2)
	var stdout, stderr bytes.Buffer

	code := finishMetrics(output.New(&stdout, &stderr, true), recorder, stateDir, true, exitcode.Success)
	if code != exitcode.Success || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
	var displayed map[string]json.RawMessage
	if err := json.Unmarshal(stderr.Bytes(), &displayed); err != nil || displayed["metrics"] == nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	content, err := os.ReadFile(filepath.Join(stateDir, "metrics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"path", "diff", "prompt", "message", "feedback", "raw-secret"} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("persisted metrics contains %q: %s", forbidden, content)
		}
	}
	for _, want := range []string{`"generation_ns":1`, `"summaries":2`, `"exit":"success"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content=%q missing %q", content, want)
		}
	}
}

func TestMainMetricsMatchFailureBoundary(t *testing.T) {
	tests := []struct {
		name           string
		failure        string
		wantCode       int
		wantExit       string
		wantVerify     bool
		wantGit        bool
		wantPush       bool
	}{
		{name: "LLM", failure: "llm", wantCode: exitcode.LLM, wantExit: "llm_error"},
		{name: "verification", failure: "verification", wantCode: exitcode.Verification, wantExit: "verification_failed", wantVerify: true},
		{name: "commit", failure: "commit", wantCode: exitcode.Commit, wantExit: "commit_failed", wantVerify: true, wantGit: true},
		{name: "push", failure: "push", wantCode: exitcode.Push, wantExit: "push_failed", wantVerify: true, wantGit: true, wantPush: true},
		{name: "SIGINT", failure: "interrupt", wantCode: exitcode.Interrupted, wantExit: "interrupted", wantVerify: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := cliRepository(t)
			cliWrite(t, repo, "a.txt", "base\n", 0o644)
			cliGit(t, repo, "add", "a.txt")
			cliGit(t, repo, "commit", "-m", "base")
			cliWrite(t, repo, "a.txt", "planned\n", 0o644)
			if test.failure == "push" {
				cliGit(t, repo, "remote", "add", "origin", "https://example.invalid/repo.git")
			}
			chdir(t, repo)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)

			if test.failure == "llm" {
				previous := planFlow
				planFlow = func(context.Context, string, gitstate.Snapshot, config.Values, string) (planning.Plan, error) {
					return planning.Plan{}, exitcode.New(exitcode.LLM, "fixture LLM failure")
				}
				t.Cleanup(func() { planFlow = previous })
			} else {
				stubPlanFlow(t, nil)
			}

			verify := func(context.Context, string, *verification.Definition, time.Duration, verification.StatePolicy) (verification.RunResult, error) {
				switch test.failure {
				case "verification":
					return verification.RunResult{}, &verification.RunError{Kind: verification.RunFailed}
				case "interrupt":
					// verification.Run reports an interrupt delivered through its
					// signal-aware context with this structured result.
					return verification.RunResult{}, &verification.RunError{Kind: verification.RunInterrupted}
				default:
					return verification.RunResult{}, nil
				}
			}
			commit := func(commitexec.Options) (commitexec.Result, error) {
				if test.failure == "commit" {
					return commitexec.Result{}, &commitexec.Error{Code: commitexec.ExitSafety, Message: "fixture commit failure"}
				}
				return commitexec.Result{Hashes: []string{"fixture-hash"}}, nil
			}
			stubPostApprovalFlows(t, verify, commit)
			stubPush(t, func(context.Context, string, interaction.PushTarget) error {
				if test.failure == "push" {
					return errors.New("fixture push failure; local commits were kept")
				}
				return nil
			})

			var stdout, stderr bytes.Buffer
			code := Run([]string{"--no-confirm-commit", "--no-confirm-push", "--record-metrics"}, &stdout, &stderr)
			if code != test.wantCode {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			record := readMetricRecord(t, filepath.Join(stateHome, "commiter", "metrics.jsonl"))
			if record.Exit != test.wantExit || record.Durations.GitPreprocessing == nil {
				t.Fatalf("record=%+v", record)
			}
			if (test.failure == "llm" && record.Durations.Generation != nil) ||
				(record.Durations.Verification != nil) != test.wantVerify ||
				(record.Durations.Git != nil) != test.wantGit ||
				(record.Durations.Push != nil) != test.wantPush {
				t.Fatalf("durations=%+v", record.Durations)
			}
		})
	}
}

func readMetricRecord(t *testing.T, path string) runmetrics.Record {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record runmetrics.Record
	if err := json.Unmarshal(bytes.TrimSpace(content), &record); err != nil {
		t.Fatalf("metrics=%q error=%v", content, err)
	}
	return record
}
