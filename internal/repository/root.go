package repository

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func Root() (string, error) {
	command := exec.Command("git", "rev-parse", "--show-toplevel")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("current directory is not inside a Git repository")
	}
	root := strings.TrimSpace(stdout.String())
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("cannot resolve repository root")
	}
	return filepath.Abs(canonical)
}
