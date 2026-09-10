package gitstate

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCollectMixedChangesWithoutReadingRejectedSensitivePaths(t *testing.T) {
	repo := newRepository(t)
	write(t, repo, "tracked.txt", "base\n", 0o644)
	write(t, repo, "partial.txt", "base\n", 0o644)
	write(t, repo, "delete.txt", "delete me\n", 0o644)
	write(t, repo, "old name.txt", "rename me\n", 0o644)
	git(t, repo, "add", "--all")
	git(t, repo, "commit", "-m", "base")

	write(t, repo, "tracked.txt", "changed\n", 0o644)
	write(t, repo, "partial.txt", "staged\n", 0o644)
	git(t, repo, "add", "partial.txt")
	write(t, repo, "partial.txt", "working final\n", 0o644)
	git(t, repo, "mv", "old name.txt", "日本語\n name.txt")
	if err := os.Remove(filepath.Join(repo, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "new.bin", "\x00binary", 0o644)
	write(t, repo, ".env", "MUST_NOT_BE_READ", 0o600)
	write(t, repo, "auth.json", "MUST_NOT_BE_READ", 0o600)

	files := &recordingFiles{blockedSuffixes: []string{".env", "auth.json"}}
	var candidates []Candidate
	first, err := Collect(repo, Options{
		Files: files,
		ApproveSensitiveCandidates: func(got []Candidate) (bool, error) {
			candidates = append([]Candidate(nil), got...)
			return false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Collect(repo, Options{Files: files})
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) != 1 || candidates[0].Path != "auth.json" {
		t.Fatalf("candidates = %#v", candidates)
	}
	if len(first.Changes) != 5 {
		t.Fatalf("changes = %#v", first.Changes)
	}
	if first.Head == "" || first.Branch != "main" || first.IndexIdentity == "" {
		t.Fatalf("snapshot metadata = %#v", first)
	}
	if !reflect.DeepEqual(first.Changes, second.Changes) || first.IndexIdentity != second.IndexIdentity {
		t.Fatalf("collection is not deterministic\nfirst=%#v\nsecond=%#v", first, second)
	}

	changes := changesByPath(first.Changes)
	partial := changes["partial.txt"]
	if !partial.Staged || !partial.Unstaged || partial.Status != "modified" || partial.WorktreeID == nil || *partial.WorktreeID != hashString("working final\n") {
		t.Fatalf("partial = %#v", partial)
	}
	rename := changes["日本語\n name.txt"]
	if rename.Status != "renamed" || rename.OldPath == nil || *rename.OldPath != "old name.txt" || rename.ID == "" {
		t.Fatalf("rename = %#v", rename)
	}
	deleted := changes["delete.txt"]
	if deleted.Status != "deleted" || deleted.NewPath != nil || deleted.WorktreeKind != "deleted" || deleted.WorktreeID != nil {
		t.Fatalf("deleted = %#v", deleted)
	}
	binary := changes["new.bin"]
	if !binary.Binary || !binary.Opaque || binary.Language != "" {
		t.Fatalf("binary = %#v", binary)
	}
	if got := excludedPaths(first.Excluded); !reflect.DeepEqual(got, []string{".env", "auth.json"}) {
		t.Fatalf("excluded = %#v", first.Excluded)
	}
	for index, change := range first.Changes {
		if change.ID != fileID(index) || len(change.ChangeHash) != 64 {
			t.Fatalf("change %d = %#v", index, change)
		}
	}
}

func TestCollectApprovesCandidateOnlyAfterPathReview(t *testing.T) {
	repo := newRepository(t)
	write(t, repo, "README.md", "base\n", 0o644)
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "base")
	write(t, repo, ".npmrc", "registry=http://loopback.invalid\n", 0o600)

	files := &recordingFiles{}
	snapshot, err := Collect(repo, Options{
		Files: files,
		ApproveSensitiveCandidates: func(candidates []Candidate) (bool, error) {
			if len(files.readPaths) != 0 {
				t.Fatalf("content read before approval: %q", files.readPaths)
			}
			if len(candidates) != 1 || candidates[0].Path != ".npmrc" {
				t.Fatalf("candidates = %#v", candidates)
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Changes) != 1 || !snapshot.Changes[0].Sensitive || snapshot.Changes[0].ID != "F001" {
		t.Fatalf("changes = %#v", snapshot.Changes)
	}
	if len(files.readPaths) != 1 || !strings.HasSuffix(files.readPaths[0], ".npmrc") {
		t.Fatalf("read paths = %q", files.readPaths)
	}
}

func TestCollectPathspecGlobsLargeFileAndSymlink(t *testing.T) {
	repo := newRepository(t)
	write(t, repo, "src/keep.go", "package keep\n", 0o644)
	write(t, repo, "src/drop.go", "package drop\n", 0o644)
	write(t, repo, "src/script.sh", "#!/bin/sh\n", 0o644)
	write(t, repo, "docs/readme.md", "docs\n", 0o644)
	if err := os.Symlink("first-target", filepath.Join(repo, "src", "link")); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "--all")
	git(t, repo, "commit", "-m", "base")
	write(t, repo, "src/keep.go", "package keep\n// changed\n", 0o644)
	write(t, repo, "src/drop.go", "package drop\n// changed\n", 0o644)
	write(t, repo, "docs/readme.md", "changed docs\n", 0o644)
	if err := os.Chmod(filepath.Join(repo, "src", "script.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "src", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("second-target", filepath.Join(repo, "src", "link")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "src/large.txt", strings.Repeat("x", LargeUntrackedSize), 0o644)

	snapshot, err := Collect(repo, Options{
		Pathspecs: []string{"src"},
		Include:   []string{"src/**"},
		Exclude:   []string{"**/drop.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	changes := changesByPath(snapshot.Changes)
	if len(changes) != 4 || changes["docs/readme.md"].ID != "" || changes["src/drop.go"].ID != "" {
		t.Fatalf("changes = %#v", snapshot.Changes)
	}
	if changes["src/keep.go"].Language != "Go" {
		t.Fatalf("language = %#v", changes["src/keep.go"])
	}
	if !changes["src/large.txt"].Opaque || changes["src/large.txt"].Binary {
		t.Fatalf("large = %#v", changes["src/large.txt"])
	}
	script := changes["src/script.sh"]
	if script.OldMode == nil || *script.OldMode != "100644" || script.NewMode == nil || *script.NewMode != "100755" {
		t.Fatalf("mode change = %#v", script)
	}
	link := changes["src/link"]
	if link.WorktreeKind != "symlink" || link.WorktreeID == nil || *link.WorktreeID != hashString("second-target") {
		t.Fatalf("link = %#v", link)
	}
}

func TestCollectSubmoduleUsesPointerWithoutWalkingFiles(t *testing.T) {
	sub := newRepository(t)
	write(t, sub, "inside.txt", "one\n", 0o644)
	git(t, sub, "add", "inside.txt")
	git(t, sub, "commit", "-m", "one")
	first := git(t, sub, "rev-parse", "HEAD")
	write(t, sub, "inside.txt", "two\n", 0o644)
	git(t, sub, "commit", "-am", "two")
	second := git(t, sub, "rev-parse", "HEAD")
	git(t, sub, "checkout", first)

	super := newRepository(t)
	git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", sub, "module")
	git(t, super, "commit", "-am", "add submodule")
	git(t, filepath.Join(super, "module"), "checkout", second)

	snapshot, err := Collect(super, Options{})
	if err != nil {
		t.Fatal(err)
	}
	changes := changesByPath(snapshot.Changes)
	module := changes["module"]
	if module.WorktreeKind != "gitlink" || module.WorktreeID == nil || *module.WorktreeID != second || module.NewMode == nil || *module.NewMode != "160000" {
		t.Fatalf("module = %#v", module)
	}
	if len(changes) != 1 {
		t.Fatalf("submodule contents were traversed: %#v", snapshot.Changes)
	}
}

func TestCollectUsesHeadToWorkingTreeFinalState(t *testing.T) {
	repo := newRepository(t)
	write(t, repo, "reverted.txt", "base\n", 0o644)
	write(t, repo, "recreated.txt", "base\n", 0o644)
	git(t, repo, "add", "--all")
	git(t, repo, "commit", "-m", "base")

	write(t, repo, "reverted.txt", "staged\n", 0o644)
	git(t, repo, "add", "reverted.txt")
	write(t, repo, "reverted.txt", "base\n", 0o644)
	git(t, repo, "rm", "recreated.txt")
	write(t, repo, "recreated.txt", "replacement\n", 0o644)

	snapshot, err := Collect(repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	changes := changesByPath(snapshot.Changes)
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", snapshot.Changes)
	}
	recreated := changes["recreated.txt"]
	if recreated.Status != "modified" || !recreated.Staged || !recreated.Unstaged || recreated.OldPath == nil || recreated.NewPath == nil {
		t.Fatalf("recreated = %#v", recreated)
	}
}

func TestSensitivePathRules(t *testing.T) {
	tests := []struct {
		path  string
		level sensitivity
	}{
		{".env", automaticallyExcluded},
		{"config/.ENV.local", automaticallyExcluded},
		{"certs/client.PEM", automaticallyExcluded},
		{".ssh/id_ed25519", automaticallyExcluded},
		{"AUTH/config.json", sensitiveCandidate},
		{"config/access-token.toml", sensitiveCandidate},
		{".docker/config.json", sensitiveCandidate},
		{"nested/kubeconfig", sensitiveCandidate},
		{"authentication.go", notSensitive},
		{"tokenizer.go", notSensitive},
		{"認証/設定.json", notSensitive},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			got, _, err := classifySensitive(test.path, nil)
			if err != nil || got != test.level {
				t.Fatalf("classifySensitive(%q) = %v, %v", test.path, got, err)
			}
		})
	}
	level, _, err := classifySensitive("safe/config.json", []string{"**/config.json"})
	if err != nil || level != automaticallyExcluded {
		t.Fatalf("additional pattern = %v, %v", level, err)
	}
}

func TestChangeHashCanonicalAndSensitiveToEveryIdentityField(t *testing.T) {
	oldPath, newPath := "old", "new"
	oldMode, newMode := "100644", "100755"
	head, working := "head-oid", "working-sha256"
	base := Change{Status: "renamed", OldPath: &oldPath, NewPath: &newPath, OldMode: &oldMode, NewMode: &newMode, HeadIdentity: &head, WorktreeKind: "file", WorktreeID: &working}
	if err := setChangeHash(&base); err != nil {
		t.Fatal(err)
	}
	if base.ChangeHash != "5d314b1c98de4ef931afca3deb9b92072ebb0e4f81c73d16f761dac1d5717e1d" {
		t.Fatalf("canonical hash = %s", base.ChangeHash)
	}

	mutations := []func(*Change){
		func(c *Change) { c.Status = "modified" },
		func(c *Change) { value := "other-old"; c.OldPath = &value },
		func(c *Change) { value := "other-new"; c.NewPath = &value },
		func(c *Change) { value := "120000"; c.OldMode = &value },
		func(c *Change) { value := "120000"; c.NewMode = &value },
		func(c *Change) { value := "other-head"; c.HeadIdentity = &value },
		func(c *Change) { c.WorktreeKind = "symlink" },
		func(c *Change) { value := "other-working"; c.WorktreeID = &value },
	}
	for index, mutate := range mutations {
		changed := base
		mutate(&changed)
		if err := setChangeHash(&changed); err != nil {
			t.Fatal(err)
		}
		if changed.ChangeHash == base.ChangeHash {
			t.Fatalf("mutation %d did not change hash", index)
		}
	}
}

func TestCollectRejectsUnsafeRepositoryStates(t *testing.T) {
	t.Run("detached HEAD", func(t *testing.T) {
		repo := committedRepository(t)
		git(t, repo, "checkout", "--detach")
		_, err := Collect(repo, Options{})
		if !IsKind(err, ErrorSafety) || !strings.Contains(err.Error(), "detached") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("index lock", func(t *testing.T) {
		repo := committedRepository(t)
		gitDirectory := git(t, repo, "rev-parse", "--git-dir")
		writeAbsolute(t, filepath.Join(repo, gitDirectory, "index.lock"), "", 0o600)
		_, err := Collect(repo, Options{})
		if !IsKind(err, ErrorSafety) || !strings.Contains(err.Error(), "index") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("operation", func(t *testing.T) {
		repo := committedRepository(t)
		gitDirectory := git(t, repo, "rev-parse", "--git-dir")
		writeAbsolute(t, filepath.Join(repo, gitDirectory, "MERGE_HEAD"), strings.Repeat("0", 40), 0o600)
		_, err := Collect(repo, Options{})
		if !IsKind(err, ErrorSafety) || !strings.Contains(err.Error(), "merge") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("commiter lock", func(t *testing.T) {
		repo := committedRepository(t)
		common := git(t, repo, "rev-parse", "--git-common-dir")
		writeAbsolute(t, filepath.Join(repo, common, "commiter.lock"), "pid\n", 0o600)
		_, err := Collect(repo, Options{})
		if !IsKind(err, ErrorSafety) || !strings.Contains(err.Error(), "another commiter") {
			t.Fatalf("error = %v", err)
		}
	})
}

type recordingFiles struct {
	readPaths       []string
	blockedSuffixes []string
}

func (files *recordingFiles) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

func (files *recordingFiles) ReadFile(path string) ([]byte, error) {
	for _, suffix := range files.blockedSuffixes {
		if strings.HasSuffix(path, suffix) {
			panic("sensitive content was read: " + suffix)
		}
	}
	files.readPaths = append(files.readPaths, path)
	return os.ReadFile(path)
}

func (files *recordingFiles) Readlink(path string) (string, error) { return os.Readlink(path) }

func newRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.invalid")
	return repo
}

func committedRepository(t *testing.T) string {
	t.Helper()
	repo := newRepository(t)
	write(t, repo, "README.md", "base\n", 0o644)
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "base")
	return repo
}

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSuffix(string(output), "\n")
}

func write(t *testing.T, repo, path, content string, mode fs.FileMode) {
	t.Helper()
	writeAbsolute(t, filepath.Join(repo, filepath.FromSlash(path)), content, mode)
}

func writeAbsolute(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func changesByPath(changes []Change) map[string]Change {
	result := make(map[string]Change, len(changes))
	for _, change := range changes {
		value := change.OldPath
		if change.NewPath != nil {
			value = change.NewPath
		}
		result[*value] = change
	}
	return result
}

func excludedPaths(excluded []Excluded) []string {
	result := make([]string, 0, len(excluded))
	for _, item := range excluded {
		result = append(result, item.Path)
	}
	sort.Strings(result)
	return result
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
