package trust

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListMissingStoreIsEmpty(t *testing.T) {
	entries, err := New(t.TempDir()).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestRevokeRemovesOnlyCanonicalRepository(t *testing.T) {
	stateDir := t.TempDir()
	repoA, repoB := t.TempDir(), t.TempDir()
	canonicalA, err := filepath.EvalSymlinks(repoA)
	if err != nil {
		t.Fatal(err)
	}
	canonicalB, err := filepath.EvalSymlinks(repoB)
	if err != nil {
		t.Fatal(err)
	}
	store := New(stateDir)
	state := fileFormat{Version: 1, Entries: []Entry{
		{RepoPath: canonicalB, DefinitionHash: "b", SourceType: "repo", Commands: [][]string{{"go", "test"}}},
		{RepoPath: canonicalA, DefinitionHash: "a", SourceType: "repo", Commands: [][]string{{"go", "test"}}},
	}}
	if err := store.write(state); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.Revoke(repoA)
	if err != nil || !revoked {
		t.Fatalf("Revoke() = %v, %v", revoked, err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].RepoPath != canonicalB {
		t.Fatalf("entries = %#v", entries)
	}
	info, err := os.Stat(filepath.Join(stateDir, "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestRevokeDeletedRepository(t *testing.T) {
	stateDir := t.TempDir()
	parent := t.TempDir()
	repo := filepath.Join(parent, "deleted-repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	store := New(stateDir)
	if err := store.write(fileFormat{Version: 1, Entries: []Entry{{RepoPath: canonical}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(repo); err != nil {
		t.Fatal(err)
	}

	revoked, err := store.Revoke(repo)
	if err != nil || !revoked {
		t.Fatalf("Revoke() = %v, %v", revoked, err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestApproveMatchesReplacesAndRevokeRequiresApprovalAgain(t *testing.T) {
	stateDir := t.TempDir()
	repo := t.TempDir()
	store := New(stateDir)
	first := Entry{
		RepoPath:       repo,
		DefinitionHash: "first",
		SourceType:     "repo_config",
		Commands:       [][]string{{"go", "test", "./..."}},
	}
	matched, err := store.Matches(repo, first.DefinitionHash)
	if err != nil || matched {
		t.Fatalf("initial Matches() = %v, %v", matched, err)
	}
	if err := store.Approve(first); err != nil {
		t.Fatal(err)
	}
	matched, err = store.Matches(repo, first.DefinitionHash)
	if err != nil || !matched {
		t.Fatalf("saved Matches() = %v, %v", matched, err)
	}
	matched, err = store.Matches(repo, "changed")
	if err != nil || matched {
		t.Fatalf("changed Matches() = %v, %v", matched, err)
	}

	first.DefinitionHash = "changed"
	first.Commands = [][]string{{"go", "vet", "./..."}}
	if err := store.Approve(first); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].DefinitionHash != "changed" {
		t.Fatalf("entries = %#v", entries)
	}
	if revoked, err := store.Revoke(repo); err != nil || !revoked {
		t.Fatalf("Revoke() = %v, %v", revoked, err)
	}
	matched, err = store.Matches(repo, "changed")
	if err != nil || matched {
		t.Fatalf("revoked Matches() = %v, %v", matched, err)
	}
}

func TestApproveAndMatchesUseCanonicalRepositoryScope(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	store := New(t.TempDir())
	entry := Entry{
		RepoPath:       link,
		DefinitionHash: "hash",
		SourceType:     "package_json_autodetect",
		Commands:       [][]string{{"npm", "run", "test"}},
	}
	if err := store.Approve(entry); err != nil {
		t.Fatal(err)
	}
	matched, err := store.Matches(root, "hash")
	if err != nil || !matched {
		t.Fatalf("Matches() = %v, %v", matched, err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].RepoPath != canonical {
		t.Fatalf("entries = %#v", entries)
	}
}
