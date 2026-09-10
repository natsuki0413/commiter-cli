package verification

import (
	"errors"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/trust"
)

func TestAuthorizeInitialSameChangedAndRevokedDefinition(t *testing.T) {
	repo := t.TempDir()
	store := trust.New(t.TempDir())
	definition := &Definition{
		SchemaVersion: 1,
		SourceType:    SourceRepoConfig,
		Commands:      []Command{{Name: "test", CWD: ".", Argv: []string{"go", "test", "./..."}}},
	}
	requests := 0
	confirm := func(request ApprovalRequest) (bool, error) {
		requests++
		if request.Hash == "" || request.Definition.SourceType != SourceRepoConfig || len(request.Definition.Commands) != 1 {
			t.Fatalf("request = %#v", request)
		}
		return true, nil
	}

	if err := Authorize(repo, definition, store, confirm); err != nil {
		t.Fatal(err)
	}
	if err := Authorize(repo, definition, store, confirm); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("same definition approval requests = %d, want 1", requests)
	}

	changed := cloneDefinition(*definition)
	changed.Commands[0].Argv = []string{"go", "test", "-race", "./..."}
	if err := Authorize(repo, &changed, store, confirm); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("changed definition approval requests = %d, want 2", requests)
	}

	if revoked, err := store.Revoke(repo); err != nil || !revoked {
		t.Fatalf("Revoke() = %v, %v", revoked, err)
	}
	if err := Authorize(repo, &changed, store, confirm); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("revoked definition approval requests = %d, want 3", requests)
	}
}

func TestAuthorizeRejectsAndDoesNotPersist(t *testing.T) {
	repo := t.TempDir()
	store := trust.New(t.TempDir())
	definition := &Definition{
		SchemaVersion: 1,
		SourceType:    SourceRepoConfig,
		Commands:      []Command{{Name: "test", CWD: ".", Argv: []string{"go", "test"}}},
	}
	err := Authorize(repo, definition, store, func(ApprovalRequest) (bool, error) { return false, nil })
	if !errors.Is(err, ErrNotApproved) {
		t.Fatalf("Authorize() error = %v", err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestAuthorizeApprovalRequestCannotChangePersistedDefinition(t *testing.T) {
	repo := t.TempDir()
	store := trust.New(t.TempDir())
	definition := &Definition{
		SchemaVersion: 1,
		SourceType:    SourcePackageJSONAutodetect,
		Commands: []Command{{
			Name: "test", CWD: ".", Argv: []string{"bun", "run", "test"},
			ManifestPath: "package.json", ScriptName: "test", ScriptBody: "vitest",
			ImplicitLifecycleScripts: []ManifestScript{{Name: "pretest", Body: "before"}},
		}},
	}
	if err := Authorize(repo, definition, store, func(request ApprovalRequest) (bool, error) {
		request.Definition.Commands[0].Argv[0] = "changed"
		request.Definition.Commands[0].ImplicitLifecycleScripts[0].Body = "changed"
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Commands[0][0] != "bun" || definition.Commands[0].Argv[0] != "bun" || definition.Commands[0].ImplicitLifecycleScripts[0].Body != "before" {
		t.Fatalf("entry = %#v, definition = %#v", entries, definition)
	}
}

func TestAuthorizeNoneDoesNotPromptOrPersist(t *testing.T) {
	store := trust.New(t.TempDir())
	called := false
	err := Authorize(t.TempDir(), nil, store, func(ApprovalRequest) (bool, error) {
		called = true
		return true, nil
	})
	if err != nil || called {
		t.Fatalf("Authorize() = %v, called=%v", err, called)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}
