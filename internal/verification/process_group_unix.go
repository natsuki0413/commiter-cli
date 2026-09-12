//go:build darwin || linux

package verification

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processTerminationGrace = 250 * time.Millisecond

func configureVerificationProcess(process *exec.Cmd) {
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process.Cancel = func() error { return cancelVerificationProcess(process) }
	process.WaitDelay = time.Second
}

func cancelVerificationProcess(process *exec.Cmd) error {
	if process.Process == nil || process.Process.Pid <= 0 {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-process.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if err != nil {
		return err
	}
	time.Sleep(processTerminationGrace)
	err = syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func cleanupVerificationProcess(process *exec.Cmd) error {
	if process.Process == nil || process.Process.Pid <= 0 {
		return nil
	}
	err := syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
