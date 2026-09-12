//go:build !darwin && !linux

package verification

import "os/exec"

func configureVerificationProcess(_ *exec.Cmd) {}

func cleanupVerificationProcess(_ *exec.Cmd) error { return nil }
