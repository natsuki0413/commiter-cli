package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type RunErrorKind int

const (
	RunFailed RunErrorKind = iota
	RunTimedOut
	RunInterrupted
)

type RunError struct {
	Kind    RunErrorKind
	Command string
}

func (e *RunError) Error() string {
	switch e.Kind {
	case RunTimedOut:
		return "verification timed out"
	case RunInterrupted:
		return "verification interrupted"
	default:
		return "verification command failed"
	}
}

type CommandResult struct {
	Name         string
	Output       string
	ChangedPaths []string
}

type RunResult struct {
	Commands []CommandResult
}

// Run executes an approved definition sequentially without invoking a shell.
// timeout applies to the complete verification sequence.
func Run(ctx context.Context, root string, definition *Definition, timeout time.Duration, policy StatePolicy) (RunResult, error) {
	if definition == nil {
		return RunResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return RunResult{}, fmt.Errorf("verification timeout must be positive")
	}
	timed, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := RunResult{Commands: make([]CommandResult, 0, len(definition.Commands))}
	for _, command := range definition.Commands {
		before, stateErr := CaptureRepositoryState(root, policy)
		if stateErr != nil {
			return result, &RunError{Kind: RunFailed, Command: command.Name}
		}
		var combined bytes.Buffer
		process := exec.CommandContext(timed, command.Argv[0], command.Argv[1:]...)
		configureVerificationProcess(process)
		process.Dir = filepath.Join(root, filepath.FromSlash(command.CWD))
		process.Env = append(os.Environ(), "LC_ALL=C")
		err := runVerificationProcess(process, &combined)
		after, afterErr := CaptureRepositoryState(root, policy)
		var changed []string
		if afterErr != nil {
			changed = []string{"<repository-state>"}
		} else {
			changed = ChangedPaths(before, after)
			if len(changed) == 0 {
				changed = nil
			}
		}
		result.Commands = append(result.Commands, CommandResult{Name: command.Name, Output: combined.String(), ChangedPaths: changed})
		if err == nil {
			if afterErr != nil {
				return result, &RunError{Kind: RunFailed, Command: command.Name}
			}
			continue
		}
		failure := &RunError{Kind: RunFailed, Command: command.Name}
		if errors.Is(timed.Err(), context.DeadlineExceeded) {
			failure.Kind = RunTimedOut
		} else if ctx.Err() != nil {
			failure.Kind = RunInterrupted
		}
		return result, failure
	}
	return result, nil
}

func runVerificationProcess(process *exec.Cmd, output io.Writer) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	process.Stdout = writer
	process.Stderr = writer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(output, reader)
		copyDone <- copyErr
	}()

	startErr := process.Start()
	_ = writer.Close()
	if startErr != nil {
		_ = reader.Close()
		<-copyDone
		return startErr
	}
	waitErr := process.Wait()
	cleanupErr := cleanupVerificationProcess(process)

	var copyErr error
	timer := time.NewTimer(time.Second)
	select {
	case copyErr = <-copyDone:
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case <-timer.C:
		_ = reader.Close()
		<-copyDone
		copyErr = exec.ErrWaitDelay
	}
	_ = reader.Close()
	if waitErr != nil {
		return waitErr
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	return copyErr
}
