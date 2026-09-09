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
