package contextinput

import (
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

func TestBuildPreservesCompleteInputInSnapshotOrder(t *testing.T) {
	onePath, twoPath := "one.go", "two.bin"
	oldMode, newMode, headID, worktreeID := "100644", "100755", "head-id", "worktree-id"
	one := gitstate.Change{ID: "F001", Status: "M", NewPath: &onePath, Language: "Go", ChangeHash: "hash-1", WorktreeKind: "file"}
	two := gitstate.Change{
		ID: "F002", Status: "?", NewPath: &twoPath, OldMode: &oldMode, NewMode: &newMode,
		HeadIdentity: &headID, WorktreeKind: "file", WorktreeID: &worktreeID,
		ChangeHash: "hash-2", Binary: true, Vendor: true, Opaque: true, Size: 42, Staged: true, Unstaged: true,
	}
	snapshot := gitstate.Snapshot{Head: "head", Branch: "main", IndexIdentity: "index", Changes: []gitstate.Change{one, two}}
	evidence := []syntax.Evidence{{Kind: "function_declaration", Name: "changed", StartLine: 1, EndLine: 2}}
	document, err := Build(snapshot, []syntax.ChangeResult{
		{Change: two, Mode: syntax.ModeMetadataOnly},
		{Change: one, Mode: syntax.ModeStructural, Evidence: evidence},
	})
	if err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.Repository.Head != "head" || len(document.Files) != 2 {
		t.Fatalf("document=%#v", document)
	}
	if document.Files[0].ID != "F001" || document.Files[0].ChangeHash != "hash-1" || len(document.Files[0].Evidence) != 1 {
		t.Fatalf("first file=%#v", document.Files[0])
	}
	if document.Files[1].ID != "F002" || !document.Files[1].Opaque || document.Files[1].RawDiff != "" ||
		document.Files[1].OldMode == nil || *document.Files[1].OldMode != oldMode ||
		document.Files[1].NewMode == nil || *document.Files[1].NewMode != newMode ||
		document.Files[1].HeadIdentity == nil || *document.Files[1].HeadIdentity != headID ||
		document.Files[1].WorktreeIdentity == nil || *document.Files[1].WorktreeIdentity != worktreeID ||
		!document.Files[1].Vendor || !document.Files[1].Staged || !document.Files[1].Unstaged {
		t.Fatalf("second file=%#v", document.Files[1])
	}
	oldMode = "changed-after-build"
	worktreeID = "changed-after-build"
	if *document.Files[1].OldMode != "100644" || *document.Files[1].WorktreeIdentity != "worktree-id" {
		t.Fatalf("document aliases mutable snapshot metadata: %#v", document.Files[1])
	}
	encoded, err := JSONRenderer(document)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("encoded=%q error=%v", encoded, err)
	}
}

func TestBuildRejectsMissingDuplicateOrStaleAnalysis(t *testing.T) {
	path := "one.go"
	change := gitstate.Change{ID: "F001", Status: "M", NewPath: &path, Language: "Go", ChangeHash: "hash-1", WorktreeKind: "file"}
	snapshot := gitstate.Snapshot{Changes: []gitstate.Change{change}}
	valid := syntax.ChangeResult{Change: change, Mode: syntax.ModeRawDiff, RawDiff: "@@ changed"}
	for name, results := range map[string][]syntax.ChangeResult{
		"missing":   nil,
		"duplicate": {valid, valid},
		"stale":     {{Change: withStatus(change, "A"), Mode: syntax.ModeRawDiff, RawDiff: "@@ changed"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Build(snapshot, results); err == nil {
				t.Fatal("invalid analysis was accepted")
			}
		})
	}
}

func TestBuildRejectsContentThatContradictsMode(t *testing.T) {
	path := "one.go"
	change := gitstate.Change{ID: "F001", Status: "M", NewPath: &path, ChangeHash: "hash", WorktreeKind: "file"}
	snapshot := gitstate.Snapshot{Changes: []gitstate.Change{change}}
	for name, result := range map[string]syntax.ChangeResult{
		"structural without evidence": {Change: change, Mode: syntax.ModeStructural},
		"raw without diff":            {Change: change, Mode: syntax.ModeRawDiff},
		"raw with evidence":           {Change: change, Mode: syntax.ModeRawDiff, Evidence: []syntax.Evidence{{Kind: "x"}}},
		"metadata with content":       {Change: change, Mode: syntax.ModeMetadataOnly, RawDiff: "private"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Build(snapshot, []syntax.ChangeResult{result}); err == nil {
				t.Fatal("contradictory input was accepted")
			}
		})
	}
}

func withStatus(change gitstate.Change, status string) gitstate.Change {
	change.Status = status
	return change
}
