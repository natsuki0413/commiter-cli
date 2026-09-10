package gitstate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type repositoryState struct {
	head         string
	branch       string
	objectFormat string
}

func inspect(root string) (repositoryState, error) {
	state := repositoryState{}
	var err error
	if state.head, err = gitText(root, "rev-parse", "--verify", "HEAD"); err != nil {
		return state, safety("repository does not have a valid HEAD")
	}
	if state.branch, err = gitText(root, "symbolic-ref", "--quiet", "--short", "HEAD"); err != nil {
		return state, safety("detached HEAD is not supported")
	}
	if state.objectFormat, err = gitText(root, "rev-parse", "--show-object-format"); err != nil {
		return state, internal("cannot determine Git object format")
	}
	if state.objectFormat != "sha1" && state.objectFormat != "sha256" {
		return state, safety("unsupported Git object format")
	}
	indexPath, err := gitPath(root, "--git-path", "index")
	if err != nil {
		return state, internal("cannot resolve Git index")
	}
	if _, err := os.Lstat(indexPath + ".lock"); err == nil {
		return state, safety("Git index is locked")
	} else if !os.IsNotExist(err) {
		return state, internal("cannot inspect Git index lock")
	}

	operationPaths := []struct {
		name string
		path string
	}{
		{"merge", "MERGE_HEAD"},
		{"rebase", "rebase-merge"},
		{"rebase", "rebase-apply"},
		{"cherry-pick", "CHERRY_PICK_HEAD"},
		{"revert", "REVERT_HEAD"},
		{"bisect", "BISECT_LOG"},
		{"sequencer", "sequencer"},
	}
	for _, operation := range operationPaths {
		path, pathErr := gitPath(root, "--git-path", operation.path)
		if pathErr != nil {
			return state, internal("cannot inspect Git operation state")
		}
		if _, statErr := os.Lstat(path); statErr == nil {
			return state, safety(operation.name + " operation is in progress")
		} else if !os.IsNotExist(statErr) {
			return state, internal("cannot inspect Git operation state")
		}
	}
	conflicts, err := gitBytes(root, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return state, internal("cannot inspect Git conflicts")
	}
	if len(conflicts) != 0 {
		return state, safety("repository has unresolved conflicts")
	}
	return state, nil
}

func gitBytes(root string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", root}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &bytes.Buffer{}
	if err := command.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func gitText(root string, args ...string) (string, error) {
	value, err := gitBytes(root, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(value), "\n"), nil
}

func gitPath(root string, args ...string) (string, error) {
	revParseArgs := append([]string{"rev-parse"}, args...)
	value, err := gitText(root, revParseArgs...)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(root, value)
	}
	return filepath.Clean(value), nil
}

func internal(message string) error { return &Error{Kind: ErrorInternal, Message: message} }
func usage(message string) error    { return &Error{Kind: ErrorUsage, Message: message} }
func safety(message string) error   { return &Error{Kind: ErrorSafety, Message: message} }

func commandFailure(action string) error {
	return internal(fmt.Sprintf("cannot %s", action))
}
