package contextinput

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

const SchemaVersion = 1

type Repository struct {
	Head          string `json:"head"`
	Branch        string `json:"branch"`
	IndexIdentity string `json:"index_identity"`
}

type File struct {
	ID               string            `json:"id"`
	Status           string            `json:"status"`
	OldPath          *string           `json:"old_path"`
	NewPath          *string           `json:"new_path"`
	OldMode          *string           `json:"old_mode"`
	NewMode          *string           `json:"new_mode"`
	HeadIdentity     *string           `json:"head_identity"`
	WorktreeKind     string            `json:"worktree_kind"`
	WorktreeIdentity *string           `json:"worktree_identity"`
	Language         string            `json:"language"`
	ChangeHash       string            `json:"change_hash"`
	Size             int64             `json:"size"`
	Binary           bool              `json:"binary"`
	Vendor           bool              `json:"vendor"`
	Opaque           bool              `json:"opaque"`
	Staged           bool              `json:"staged"`
	Unstaged         bool              `json:"unstaged"`
	Mode             syntax.Mode       `json:"mode"`
	Evidence         []syntax.Evidence `json:"evidence,omitempty"`
	RawDiff          string            `json:"raw_diff,omitempty"`
	Summary          string            `json:"summary,omitempty"`
}

type Document struct {
	SchemaVersion int        `json:"schema_version"`
	Repository    Repository `json:"repository"`
	Files         []File     `json:"files"`
}

type Renderer func(Document) ([]byte, error)

// Build joins the immutable Git snapshot to syntax results. Every collected
// change must occur exactly once, and analysis metadata must still match it.
func Build(snapshot gitstate.Snapshot, results []syntax.ChangeResult) (Document, error) {
	byID := make(map[string]syntax.ChangeResult, len(results))
	for _, result := range results {
		if result.Change.ID == "" || result.Change.ChangeHash == "" {
			return Document{}, errors.New("analysis result is missing file identity")
		}
		if _, exists := byID[result.Change.ID]; exists {
			return Document{}, fmt.Errorf("duplicate analysis result for file %s", result.Change.ID)
		}
		byID[result.Change.ID] = result
	}
	if len(byID) != len(snapshot.Changes) {
		return Document{}, errors.New("analysis results do not cover the complete file set")
	}

	document := Document{
		SchemaVersion: SchemaVersion,
		Repository: Repository{
			Head: snapshot.Head, Branch: snapshot.Branch, IndexIdentity: snapshot.IndexIdentity,
		},
		Files: make([]File, 0, len(snapshot.Changes)),
	}
	seen := make(map[string]bool, len(snapshot.Changes))
	for _, change := range snapshot.Changes {
		if change.ID == "" || change.ChangeHash == "" || seen[change.ID] {
			return Document{}, errors.New("snapshot contains an invalid file identity")
		}
		seen[change.ID] = true
		result, ok := byID[change.ID]
		if !ok || !reflect.DeepEqual(result.Change, change) {
			return Document{}, fmt.Errorf("analysis metadata does not match file %s", change.ID)
		}
		if err := validateMode(result); err != nil {
			return Document{}, fmt.Errorf("file %s: %w", change.ID, err)
		}
		document.Files = append(document.Files, File{
			ID: change.ID, Status: change.Status, OldPath: cloneString(change.OldPath), NewPath: cloneString(change.NewPath),
			OldMode: cloneString(change.OldMode), NewMode: cloneString(change.NewMode), HeadIdentity: cloneString(change.HeadIdentity),
			WorktreeKind: change.WorktreeKind, WorktreeIdentity: cloneString(change.WorktreeID),
			Language: change.Language, ChangeHash: change.ChangeHash, Size: change.Size,
			Binary: change.Binary, Vendor: change.Vendor, Opaque: change.Opaque,
			Staged: change.Staged, Unstaged: change.Unstaged, Mode: result.Mode,
			Evidence: append([]syntax.Evidence(nil), result.Evidence...), RawDiff: result.RawDiff,
		})
	}
	return document, nil
}

func validateMode(result syntax.ChangeResult) error {
	switch result.Mode {
	case syntax.ModeStructural:
		if len(result.Evidence) == 0 || result.RawDiff != "" {
			return errors.New("structural input must contain evidence and no raw diff")
		}
	case syntax.ModeRawDiff:
		if len(result.Evidence) != 0 || result.RawDiff == "" {
			return errors.New("raw-diff input must contain a diff and no structural evidence")
		}
	case syntax.ModeMetadataOnly:
		if len(result.Evidence) != 0 || result.RawDiff != "" {
			return errors.New("metadata-only input must not contain content")
		}
	default:
		return errors.New("analysis mode is invalid")
	}
	return nil
}

func JSONRenderer(document Document) ([]byte, error) {
	return json.Marshal(document)
}

// ValidatePreserved ensures summaries cannot remove, duplicate, or rewrite Git
// identities or structural evidence. Only RawDiff and Summary may change.
func ValidatePreserved(original, summarized Document) error {
	if original.SchemaVersion != summarized.SchemaVersion || original.Repository != summarized.Repository {
		return errors.New("summary changed repository identity")
	}
	if len(original.Files) != len(summarized.Files) {
		return errors.New("summary changed the file set")
	}
	originalByID := make(map[string]File, len(original.Files))
	for _, file := range original.Files {
		if _, exists := originalByID[file.ID]; exists {
			return errors.New("original input contains duplicate file IDs")
		}
		originalByID[file.ID] = file
	}
	seen := make(map[string]bool, len(summarized.Files))
	for _, file := range summarized.Files {
		base, ok := originalByID[file.ID]
		if !ok || seen[file.ID] {
			return errors.New("summary changed the file ID set")
		}
		seen[file.ID] = true
		base.RawDiff, base.Summary = "", ""
		file.RawDiff, file.Summary = "", ""
		if !reflect.DeepEqual(base, file) {
			return fmt.Errorf("summary changed required data for file %s", file.ID)
		}
	}
	return nil
}
