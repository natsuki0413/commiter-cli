package gitstate

import "errors"

const (
	HashSchemaVersion  = 1
	LargeUntrackedSize = 64 * 1024
)

type ErrorKind int

const (
	ErrorInternal ErrorKind = iota
	ErrorUsage
	ErrorSafety
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

type Candidate struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Excluded struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Change struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	OldPath      *string `json:"old_path"`
	NewPath      *string `json:"new_path"`
	OldMode      *string `json:"old_mode"`
	NewMode      *string `json:"new_mode"`
	HeadIdentity *string `json:"head_identity"`
	WorktreeKind string  `json:"worktree_kind"`
	WorktreeID   *string `json:"worktree_identity"`
	Language     string  `json:"language"`
	Size         int64   `json:"size"`
	Binary       bool    `json:"binary"`
	Vendor       bool    `json:"vendor"`
	Opaque       bool    `json:"opaque"`
	Sensitive    bool    `json:"sensitive_candidate"`
	Staged       bool    `json:"staged"`
	Unstaged     bool    `json:"unstaged"`
	ChangeHash   string  `json:"change_hash"`
}

type Snapshot struct {
	Root          string     `json:"root"`
	Head          string     `json:"head"`
	Branch        string     `json:"branch"`
	IndexIdentity string     `json:"index_identity"`
	Untracked     []string   `json:"untracked"`
	Changes       []Change   `json:"changes"`
	Excluded      []Excluded `json:"excluded"`
}

type Options struct {
	Pathspecs                  []string
	Include                    []string
	Exclude                    []string
	AdditionalSensitiveGlobs   []string
	ApproveSensitiveCandidates func([]Candidate) (bool, error)
	Files                      FileReader
}
