package planning

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

func Validate(candidate []byte, fileIDs []string, sensitive SensitiveValues) (Plan, []Violation) {
	violations := make([]Violation, 0, 4)
	var plan Plan
	if !json.Valid(candidate) {
		violations = append(violations, InvalidJSON)
	} else {
		var decoded wirePlan
		decoder := json.NewDecoder(bytes.NewReader(candidate))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&decoded); err != nil {
			violations = append(violations, InvalidSchema)
		} else if err := requireEOF(decoder); err != nil {
			violations = append(violations, InvalidJSON)
		} else {
			var valid bool
			plan, valid = decoded.plan()
			if !valid {
				violations = append(violations, InvalidSchema)
			}
		}
	}
	for _, commit := range plan.Commits {
		if sensitive.Contains([]byte(commit.Scope)) || sensitive.Contains([]byte(commit.Summary)) {
			violations = append(violations, SensitiveOutput)
		}
	}
	if containsViolation(violations, InvalidJSON) || containsViolation(violations, InvalidSchema) {
		return Plan{}, uniqueViolations(violations)
	}
	if plan.SchemaVersion != SchemaVersion || len(plan.Commits) == 0 {
		violations = append(violations, InvalidSchema)
	}
	allowed := make(map[string]bool, len(allowedTypes))
	for _, value := range allowedTypes {
		allowed[value] = true
	}
	for _, commit := range plan.Commits {
		if !allowed[commit.Type] {
			violations = append(violations, InvalidType)
		}
		if !oneNonEmptyLine(commit.Scope) {
			violations = append(violations, InvalidScope)
		}
		if !oneNonEmptyLine(commit.Summary) {
			violations = append(violations, InvalidSummary)
		}
		if len(commit.FileIDs) == 0 {
			violations = append(violations, InvalidAssignment)
		}
	}
	if !completeAssignment(plan, fileIDs) {
		violations = append(violations, InvalidAssignment)
	}
	return plan, uniqueViolations(violations)
}

type wirePlan struct {
	SchemaVersion int          `json:"schema_version"`
	Commits       []wireCommit `json:"commits"`
}

type wireCommit struct {
	Type     string          `json:"type"`
	Scope    string          `json:"scope"`
	Breaking json.RawMessage `json:"breaking"`
	Summary  string          `json:"summary"`
	FileIDs  []string        `json:"file_ids"`
}

func (decoded wirePlan) plan() (Plan, bool) {
	plan := Plan{SchemaVersion: decoded.SchemaVersion, Commits: make([]Commit, len(decoded.Commits))}
	valid := true
	for index, source := range decoded.Commits {
		var breaking bool
		if len(source.Breaking) == 0 || bytes.Equal(bytes.TrimSpace(source.Breaking), []byte("null")) || json.Unmarshal(source.Breaking, &breaking) != nil {
			valid = false
		}
		plan.Commits[index] = Commit{
			Type: source.Type, Scope: source.Scope, Breaking: breaking,
			Summary: source.Summary, FileIDs: source.FileIDs,
		}
	}
	return plan, valid
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("candidate contains more than one JSON value")
	}
	return err
}

func oneNonEmptyLine(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.ContainsAny(value, "\r\n")
}

func completeAssignment(plan Plan, fileIDs []string) bool {
	wanted := make(map[string]bool, len(fileIDs))
	for _, id := range fileIDs {
		if id == "" || wanted[id] {
			return false
		}
		wanted[id] = true
	}
	assigned := make(map[string]bool, len(fileIDs))
	for _, commit := range plan.Commits {
		for _, id := range commit.FileIDs {
			if !wanted[id] || assigned[id] {
				return false
			}
			assigned[id] = true
		}
	}
	return len(assigned) == len(wanted)
}

func containsViolation(violations []Violation, wanted Violation) bool {
	for _, violation := range violations {
		if violation == wanted {
			return true
		}
	}
	return false
}

func uniqueViolations(violations []Violation) []Violation {
	result := make([]Violation, 0, len(violations))
	seen := make(map[Violation]bool, len(violations))
	for _, violation := range violations {
		if !seen[violation] {
			seen[violation] = true
			result = append(result, violation)
		}
	}
	return result
}
