// Package planning renders constrained local-LLM requests and validates commit
// plans before any approval or Git mutation is possible.
package planning

import "fmt"

const SchemaVersion = 1

type Language string

const (
	English  Language = "en"
	Japanese Language = "ja"
)

type Plan struct {
	SchemaVersion int      `json:"schema_version"`
	Commits       []Commit `json:"commits"`
}

type Commit struct {
	Type     string   `json:"type"`
	Scope    string   `json:"scope"`
	Breaking bool     `json:"breaking"`
	Summary  string   `json:"summary"`
	FileIDs  []string `json:"file_ids"`
}

func (commit Commit) Subject() string {
	mark := ""
	if commit.Breaking {
		mark = "!"
	}
	return fmt.Sprintf("%s(%s)%s: %s", commit.Type, commit.Scope, mark, commit.Summary)
}

type Result struct {
	Plan     Plan
	Calls    int
	Repaired bool
}

type Violation string

const (
	InvalidJSON       Violation = "invalid_json"
	InvalidSchema     Violation = "invalid_schema"
	InvalidType       Violation = "invalid_type"
	InvalidScope      Violation = "invalid_scope"
	InvalidSummary    Violation = "invalid_summary"
	InvalidAssignment Violation = "invalid_assignment"
	SensitiveOutput   Violation = "sensitive_output"
)
