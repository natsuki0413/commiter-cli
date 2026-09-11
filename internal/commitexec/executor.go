// Package commitexec applies an already validated commit plan while preserving
// the caller's index selections outside the planned files.
package commitexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/planning"
)

// ExitCode is the structured classification for commit execution failures.
const (
	ExitSafety      = 7
	ExitInterrupted = 130
)

type Error struct {
	Code       int
	Message    string
	CommitHash string
	Paths      []string
	HookOutput string
	Restored   bool
}

func (e *Error) Error() string { return e.Message }

// Options describes one approved plan. Changes must be the same snapshot that
// was used to validate the plan; IDs are resolved again immediately before each
// stage operation.
type Options struct {
	Context context.Context
	Root    string
	Changes []gitstate.Change
	Plan    planning.Plan
	Writer  io.Writer
}

type Result struct {
	Hashes []string
}

var restoreInitialIndex = restoreIndex
var restoreOutsideIndex = restoreOutOfScopeIndex
var readHEAD = func(root string) (string, error) {
	return gitText(root, "rev-parse", "HEAD")
}

// Execute creates commits in plan order. It never invokes reset, stash, amend,
// force, or --no-verify. The original index is restored on any pre-commit
// failure or interruption. A post-commit invariant failure deliberately keeps
// created commits and returns the created hash in Error.CommitHash.
func Execute(options Options) (result Result, returnErr error) {
	if options.Root == "" || len(options.Changes) == 0 || len(options.Plan.Commits) == 0 {
		return Result{}, &Error{Code: ExitSafety, Message: "commit execution requires a non-empty repository, changes, and plan"}
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return Result{}, err
	}
	original, err := saveIndex(root)
	if err != nil {
		return Result{}, err
	}
	committedPaths := []string{}
	commitCreated := false
	defer func() {
		if returnErr == nil {
			return
		}
		var restoreErr error
		if !commitCreated {
			restoreErr = restoreInitialIndex(root, original)
		} else {
			restoreErr = restoreOutsideIndex(root, original, committedPaths)
		}
		if restoreErr != nil {
			returnErr = &Error{Code: ExitSafety, Message: "commit failed and index restoration is unknown", Restored: false}
			return
		}
		if failure, ok := returnErr.(*Error); ok {
			failure.Restored = true
		}
	}()
	if err := interruption(ctx); err != nil {
		return result, err
	}

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
		if err := interruption(ctx); err != nil {
			return result, err
		}
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
		parent, err := readHEAD(root)
		if err != nil {
			return result, err
		}
		cmd := exec.CommandContext(ctx, "git", "-C", root, "commit", "-m", message)
		var hookOutput bytes.Buffer
		cmd.Stdout = &hookOutput
		cmd.Stderr = &hookOutput
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if err := cmd.Run(); err != nil {
			created := ""
			if current, headErr := readHEAD(root); headErr == nil && current != parent {
				created = current
				commitCreated = true
				result.Hashes = append(result.Hashes, current)
				committedPaths = append(committedPaths, paths...)
				if failure := validateCreatedCommit(root, parent, current, paths); failure != nil {
					failure.HookOutput = output.Escape(hookOutput.String())
					return result, failure
				}
			}
			if ctx.Err() != nil {
				return result, &Error{Code: ExitInterrupted, Message: "commit execution interrupted", CommitHash: created, HookOutput: output.Escape(hookOutput.String())}
			}
			return result, &Error{Code: ExitSafety, Message: "git commit failed", CommitHash: created, HookOutput: output.Escape(hookOutput.String())}
		}
		commitCreated = true
		committedPaths = append(committedPaths, paths...)
		hash, err := readHEAD(root)
		if err != nil {
			return result, &Error{Code: ExitSafety, Message: "commit was created but its hash could not be determined"}
		}
		result.Hashes = append(result.Hashes, hash)
		if failure := validateCreatedCommit(root, parent, hash, paths); failure != nil {
			failure.HookOutput = output.Escape(hookOutput.String())
			return result, failure
		}
		if err := restoreOutOfScopeIndex(root, original, unique(committedPaths)); err != nil {
			return result, err
		}
		if hookOutput.Len() > 0 && options.Writer != nil {
			if _, err := io.WriteString(options.Writer, output.Escape(hookOutput.String())); err != nil {
				return result, &Error{Code: ExitSafety, Message: "commit was created but hook output could not be written", CommitHash: hash}
			}
		}
	}
	if len(assigned) != len(allIDs) {
		return result, &Error{Code: ExitSafety, Message: "commit plan does not assign every change"}
	}
	return result, nil
}

func validateCreatedCommit(root, parent, hash string, paths []string) *Error {
	actualParent, err := gitText(root, "rev-parse", hash+"^")
	if err != nil {
		return &Error{Code: ExitSafety, Message: "cannot inspect created commit parent", CommitHash: hash}
	}
	if actualParent != parent {
		return &Error{Code: ExitSafety, Message: "created commit parent does not match expected HEAD", CommitHash: hash}
	}
	actual, err := commitPaths(root, hash)
	if err != nil {
		return &Error{Code: ExitSafety, Message: "cannot inspect created commit file set", CommitHash: hash}
	}
	expected := pathSet(paths)
	if !sameSet(actual, expected) {
		return &Error{Code: ExitSafety, Message: "commit file set does not match its assignment", CommitHash: hash, Paths: symmetricDifference(actual, expected)}
	}
	return nil
}

func interruption(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return &Error{Code: ExitInterrupted, Message: "commit execution interrupted"}
	}
	return nil
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
	for _, path := range unique(planned) {
		entry, err := gitBytes(root, "ls-tree", "-z", "HEAD", "--", path)
		if err != nil {
			return err
		}
		if len(entry) == 0 {
			if err := runGit(root, env, "update-index", "--force-remove", "--", path); err != nil {
				return err
			}
			continue
		}
		mode, object, ok := parseTreeEntry(entry, path)
		if !ok {
			return fmt.Errorf("cannot restore committed path in index")
		}
		if err := runGit(root, env, "update-index", "--add", "--cacheinfo", mode, object, path); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath(root), data, 0o600)
}

func parseTreeEntry(entry []byte, path string) (string, string, bool) {
	entry = bytes.TrimSuffix(entry, []byte{0})
	tab := bytes.IndexByte(entry, '\t')
	if tab < 0 || string(entry[tab+1:]) != path {
		return "", "", false
	}
	fields := strings.Fields(string(entry[:tab]))
	if len(fields) != 3 {
		return "", "", false
	}
	return fields[0], fields[2], true
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
	return changePaths(change)
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
func symmetricDifference(left, right map[string]bool) []string {
	var result []string
	for key := range left {
		if !right[key] {
			result = append(result, key)
		}
	}
	for key := range right {
		if !left[key] {
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
