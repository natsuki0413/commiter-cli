// Package commitexec applies an already validated commit plan while preserving
// the caller's index selections outside the planned files.
package commitexec

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/planning"
)

// ExitCode is the structured classification for commit execution failures.
const (
	ExitSafety = 7
)

type Error struct {
	Code       int
	Message    string
	CommitHash string
	Paths      []string
	HookOutput string
}

func (e *Error) Error() string { return e.Message }

// Options describes one approved plan. Changes must be the same snapshot that
// was used to validate the plan; IDs are resolved again immediately before each
// stage operation.
type Options struct {
	Root    string
	Changes []gitstate.Change
	Plan    planning.Plan
	Writer  io.Writer
}

type Result struct {
	Hashes []string
}

// Execute creates commits in plan order. It never invokes reset, stash, amend,
// force, or --no-verify. The original index is restored on any pre-commit
// failure or interruption. A post-commit invariant failure deliberately keeps
// created commits and returns the created hash in Error.CommitHash.
func Execute(options Options) (Result, error) {
	if options.Root == "" || len(options.Changes) == 0 || len(options.Plan.Commits) == 0 {
		return Result{}, &Error{Code: ExitSafety, Message: "commit execution requires a non-empty repository, changes, and plan"}
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return Result{}, err
	}
	original, err := saveIndex(root)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	committedPaths := []string{}
	failedBeforeCommit := true
	defer func() {
		if failedBeforeCommit {
			_ = restoreIndex(root, original)
		}
	}()

	allIDs := make(map[string]bool, len(options.Changes))
	byID := make(map[string]gitstate.Change, len(options.Changes))
	for _, change := range options.Changes {
		if change.ID == "" || allIDs[change.ID] {
			return Result{}, &Error{Code: ExitSafety, Message: "commit plan contains invalid or duplicate file IDs"}
		}
		allIDs[change.ID] = true
		byID[change.ID] = change
	}
	assigned := make(map[string]bool)
	for _, commit := range options.Plan.Commits {
		if len(commit.FileIDs) == 0 {
			return result, &Error{Code: ExitSafety, Message: "commit plan contains an empty file assignment"}
		}
		current := make(map[string]gitstate.Change, len(commit.FileIDs))
		paths := make([]string, 0, len(commit.FileIDs))
		for _, id := range commit.FileIDs {
			change, ok := byID[id]
			if !ok || assigned[id] || current[id].ID != "" {
				return result, &Error{Code: ExitSafety, Message: "commit plan has an invalid or repeated file assignment"}
			}
			current[id] = change
			assigned[id] = true
			paths = append(paths, stagePaths(change)...)
		}
		if err := verifyHashes(root, options.Changes, commit.FileIDs); err != nil {
			return result, err
		}
		if err := stageAssignment(root, original, allPlannedPaths(options.Changes), paths); err != nil {
			return result, err
		}
		message := commit.Subject()
		cmd := exec.Command("git", "-C", root, "commit", "-m", message)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if err := cmd.Run(); err != nil {
			return result, &Error{Code: ExitSafety, Message: "git commit failed", HookOutput: terminalSafe(output.String())}
		}
		hash, err := gitText(root, "rev-parse", "HEAD")
		if err != nil {
			return result, err
		}
		actual, err := commitPaths(root, hash)
		if err != nil {
			return result, err
		}
		expected := pathSet(paths)
		if !sameSet(actual, expected) {
			return result, &Error{Code: ExitSafety, Message: "commit file set does not match its assignment", CommitHash: hash, Paths: sortedSetDifference(actual, expected)}
		}
		result.Hashes = append(result.Hashes, hash)
		committedPaths = append(committedPaths, paths...)
		if err := restoreOutOfScopeIndex(root, original, unique(committedPaths)); err != nil {
			return result, err
		}
	}
	if len(assigned) != len(allIDs) {
		return result, &Error{Code: ExitSafety, Message: "commit plan does not assign every change"}
	}
	failedBeforeCommit = false
	return result, nil
}

func verifyHashes(root string, changes []gitstate.Change, ids []string) error {
	current, err := gitstate.Collect(root, gitstate.Options{})
	if err != nil {
		return &Error{Code: ExitSafety, Message: "cannot revalidate changes before staging"}
	}
	for _, id := range ids {
		var expected gitstate.Change
		for _, change := range changes {
			if change.ID == id {
				expected = change
				break
			}
		}
		found := false
		for _, candidate := range current.Changes {
			if changeKey(candidate) == changeKey(expected) && candidate.ChangeHash == expected.ChangeHash {
				found = true
				break
			}
		}
		if expected.ID == "" || !found {
			return &Error{Code: ExitSafety, Message: "change hash changed before staging"}
		}
	}
	return nil
}

func stageAssignment(root string, original []byte, allPlanned, current []string) error {
	if err := os.WriteFile(indexPath(root), original, 0o600); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(indexPath(root)), "commiter-index-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if _, err = temp.Write(original); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return err
	}
	if err = temp.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}
	defer os.Remove(tempPath)
	env := append(os.Environ(), "GIT_INDEX_FILE="+tempPath, "LC_ALL=C")
	if err := runGit(root, env, "read-tree", "HEAD"); err != nil {
		return fmt.Errorf("prepare commit index: %w", err)
	}
	for _, path := range unique(current) {
		if err := runGit(root, env, "add", "--", path); err != nil {
			return fmt.Errorf("stage assigned path: %w", err)
		}
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath(root), data, 0o600)
}

func restoreOutOfScopeIndex(root string, original []byte, planned []string) error {
	temp, err := os.CreateTemp(filepath.Dir(indexPath(root)), "commiter-retain-index-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if _, err = temp.Write(original); err != nil {
		temp.Close()
		os.Remove(tempPath)
		return err
	}
	if err = temp.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}
	defer os.Remove(tempPath)
	env := append(os.Environ(), "GIT_INDEX_FILE="+tempPath, "LC_ALL=C")
	for _, path := range planned {
		if err := runGit(root, env, "add", "--", path); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath(root), data, 0o600)
}

func saveIndex(root string) ([]byte, error)       { return os.ReadFile(indexPath(root)) }
func restoreIndex(root string, data []byte) error { return os.WriteFile(indexPath(root), data, 0o600) }
func indexPath(root string) string {
	value, _ := gitText(root, "rev-parse", "--git-path", "index")
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, value)
}

func changePaths(change gitstate.Change) []string {
	result := []string{}
	if change.OldPath != nil {
		result = append(result, *change.OldPath)
	}
	if change.NewPath != nil {
		result = append(result, *change.NewPath)
	}
	return unique(result)
}
func changeKey(change gitstate.Change) string {
	oldPath, newPath := "", ""
	if change.OldPath != nil {
		oldPath = *change.OldPath
	}
	if change.NewPath != nil {
		newPath = *change.NewPath
	}
	return change.Status + "\x00" + oldPath + "\x00" + newPath
}
func stagePaths(change gitstate.Change) []string {
	if change.NewPath != nil {
		return []string{*change.NewPath}
	}
	if change.OldPath != nil {
		return []string{*change.OldPath}
	}
	return nil
}
func allPlannedPaths(changes []gitstate.Change) []string {
	var result []string
	for _, c := range changes {
		result = append(result, changePaths(c)...)
	}
	return unique(result)
}
func unique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func pathSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
func sameSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}
func sortedSetDifference(left, right map[string]bool) []string {
	var result []string
	for key := range left {
		if !right[key] {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func commitPaths(root, hash string) (map[string]bool, error) {
	value, err := gitBytes(root, "diff-tree", "--root", "--no-commit-id", "--name-only", "-z", "-r", hash)
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, path := range bytes.Split(value, []byte{0}) {
		if len(path) > 0 {
			result[string(path)] = true
		}
	}
	return result, nil
}
func runGit(root string, env []string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = env
	return cmd.Run()
}
func runGitInput(root string, env []string, input []byte, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	return cmd.Run()
}
func gitBytes(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd.Output()
}
func gitText(root string, args ...string) (string, error) {
	value, err := gitBytes(root, args...)
	return strings.TrimSpace(string(value)), err
}
func terminalSafe(value string) string {
	value = strings.ToValidUTF8(value, "")
	var b strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
