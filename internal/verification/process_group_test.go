//go:build darwin || linux

package verification

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunStopsVerificationProcessGroupOnTimeout(t *testing.T) {
	repo, definition := processGroupFixture(t)
	started := time.Now()
	result, err := Run(context.Background(), repo, definition, 3*time.Second, StatePolicy{})
	assertStoppedProcessGroup(t, repo, result, err, RunTimedOut, time.Since(started))
}

func TestRunStopsVerificationProcessGroupOnInterruption(t *testing.T) {
	repo, definition := processGroupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan bool, 1)
	go func() {
		ready <- waitForPath(filepath.Join(repo, "child.pid"), 5*time.Second)
		cancel()
	}()

	started := time.Now()
	result, err := Run(ctx, repo, definition, 10*time.Second, StatePolicy{})
	if !<-ready {
		t.Fatal("child process did not start before interruption")
	}
	assertStoppedProcessGroup(t, repo, result, err, RunInterrupted, time.Since(started))
}

func TestRunCleansUpDescendantAfterSuccessfulParentExit(t *testing.T) {
	repo := verificationRepository(t)
	script := `#!/bin/sh
sh -c 'trap "" TERM; printf "%s" "$$" > child.pid; sleep 30; printf survived > survived.txt' &
while [ ! -f child.pid ]; do sleep 0.01; done
printf ok
`
	writeVerificationFile(t, repo, "early-exit.sh", script)
	definition := &Definition{Commands: []Command{{Name: "early-exit", CWD: ".", Argv: []string{"sh", "early-exit.sh"}}}}

	started := time.Now()
	result, err := Run(context.Background(), repo, definition, 10*time.Second, StatePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= 5*time.Second {
		t.Fatalf("successful parent waited for descendant: %v", time.Since(started))
	}
	if len(result.Commands) != 1 || result.Commands[0].Output != "ok" {
		t.Fatalf("result = %#v", result)
	}
	assertFixtureDescendantStopped(t, repo)
}

func TestCancelTreatsMissingProcessGroupAsAlreadyDone(t *testing.T) {
	process, err := os.FindProcess(1 << 30)
	if err != nil {
		t.Fatal(err)
	}
	command := &exec.Cmd{Process: process}
	if err := cancelVerificationProcess(command); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("cancel error = %v", err)
	}
}

func processGroupFixture(t *testing.T) (string, *Definition) {
	t.Helper()
	repo := verificationRepository(t)
	script := `#!/bin/sh
trap 'exit 0' TERM
sh -c 'trap "" TERM; printf "%s" "$$" > child.pid; sleep 30; printf survived > survived.txt' &
printf started
wait
`
	writeVerificationFile(t, repo, "process-tree.sh", script)
	return repo, &Definition{Commands: []Command{{Name: "process-tree", CWD: ".", Argv: []string{"sh", "process-tree.sh"}}}}
}

func assertStoppedProcessGroup(t *testing.T, repo string, result RunResult, runErr error, wantKind RunErrorKind, elapsed time.Duration) {
	t.Helper()
	var failure *RunError
	if !errors.As(runErr, &failure) || failure.Kind != wantKind {
		t.Fatalf("error = %#v, want kind %v", runErr, wantKind)
	}
	if elapsed >= 8*time.Second {
		t.Fatalf("process tree shutdown took %v", elapsed)
	}
	if len(result.Commands) != 1 || !strings.Contains(result.Commands[0].Output, "started") {
		t.Fatalf("result = %#v", result)
	}
	assertFixtureDescendantStopped(t, repo)
}

func assertFixtureDescendantStopped(t *testing.T, repo string) {
	t.Helper()
	pidBytes, err := os.ReadFile(filepath.Join(repo, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	if !waitForProcessExit(pid, 2*time.Second) {
		t.Fatalf("descendant process %d survived cancellation", pid)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(repo, "survived.txt")); !os.IsNotExist(err) {
		t.Fatalf("descendant continued after Run returned: %v", err)
	}
}

func waitForPath(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
