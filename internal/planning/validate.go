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
		decoder := json.NewDecoder(bytes.NewReader(candidate))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&plan); err != nil {
			violations = append(violations, InvalidSchema)
		} else if err := requireEOF(decoder); err != nil {
			violations = append(violations, InvalidJSON)
		}
	}
	if sensitive.Contains(candidate) {
		violations = append(violations, SensitiveOutput)
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
