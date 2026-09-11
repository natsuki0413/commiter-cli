package planning

import (
	"encoding/json"
	"errors"
)

var allowedTypes = []string{
	"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert",
}

type Constraints struct {
	SchemaVersion   int      `json:"schema_version"`
	Language        Language `json:"summary_language"`
	AllowedTypes    []string `json:"allowed_types"`
	RequiredFileIDs []string `json:"required_file_ids"`
	Assignment      string   `json:"assignment"`
	Grouping        string   `json:"grouping"`
	OpaqueGrouping  string   `json:"opaque_grouping"`
	Summary         string   `json:"summary"`
}

func NewConstraints(language Language, fileIDs []string) (Constraints, error) {
	if err := validateLanguage(language); err != nil {
		return Constraints{}, err
	}
	ids, err := exactIDs(fileIDs)
	if err != nil {
		return Constraints{}, err
	}
	return Constraints{
		SchemaVersion: SchemaVersion, Language: language,
		AllowedTypes: append([]string(nil), allowedTypes...), RequiredFileIDs: ids,
		Assignment:     "assign every required_file_id to exactly one commit; do not invent IDs or split a file",
		Grouping:       "infer purpose from actual change content; preserve every legal grouping and order returned by the model",
		OpaqueGrouping: "only opaque files may use path, status, size, type, and other metadata as supporting grouping evidence",
		Summary:        "write one non-empty line with no commit body",
	}, nil
}

func ConstraintsJSON(language Language, fileIDs []string) ([]byte, error) {
	constraints, err := NewConstraints(language, fileIDs)
	if err != nil {
		return nil, err
	}
	return json.Marshal(constraints)
}

func Schema(fileIDs []string) (json.RawMessage, error) {
	ids, err := exactIDs(fileIDs)
	if err != nil {
		return nil, err
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer", "const": SchemaVersion},
			"commits": map[string]any{
				"type": "array", "minItems": 1,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{
						"type":     map[string]any{"type": "string", "enum": allowedTypes},
						"scope":    map[string]any{"type": "string", "minLength": 1, "pattern": `^[^\r\n]+$`},
						"breaking": map[string]any{"type": "boolean"},
						"summary":  map[string]any{"type": "string", "minLength": 1, "pattern": `^[^\r\n]+$`},
						"file_ids": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": ids}},
					},
					"required": []string{"type", "scope", "breaking", "summary", "file_ids"},
				},
			},
		},
		"required": []string{"schema_version", "commits"},
	}
	encoded, err := json.Marshal(schema)
	return json.RawMessage(encoded), err
}

func validateLanguage(language Language) error {
	if language != English && language != Japanese {
		return errors.New("planning language must be en or ja")
	}
	return nil
}

func exactIDs(fileIDs []string) ([]string, error) {
	if len(fileIDs) == 0 {
		return nil, errors.New("planning requires at least one file ID")
	}
	seen := make(map[string]bool, len(fileIDs))
	ids := make([]string, len(fileIDs))
	for index, id := range fileIDs {
		if id == "" || seen[id] {
			return nil, errors.New("planning file IDs must be non-empty and unique")
		}
		seen[id] = true
		ids[index] = id
	}
	return ids, nil
}
