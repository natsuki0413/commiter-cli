package planning

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/contextinput"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/ollama"
	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

func TestRendererBuildsDeterministicUntrustedDataEnvelope(t *testing.T) {
	document := planningDocument()
	english, err := Renderer(English)(document)
	if err != nil {
		t.Fatal(err)
	}
	japanese, err := Renderer(Japanese)(document)
	if err != nil {
		t.Fatal(err)
	}
	if bytesEqual(english, japanese) {
		t.Fatal("language did not change the prompt")
	}
	for language, encoded := range map[string][]byte{"en": english, "ja": japanese} {
		var envelope struct {
			TrustBoundary string          `json:"trust_boundary"`
			Constraints   json.RawMessage `json:"constraints"`
		}
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatal(err)
		}
		var constraints Constraints
		if err := json.Unmarshal(envelope.Constraints, &constraints); err != nil {
			t.Fatal(err)
		}
		if constraints.Language != Language(language) || !reflect.DeepEqual(constraints.RequiredFileIDs, []string{"F001", "F002"}) || !strings.Contains(envelope.TrustBoundary, "untrusted") {
			t.Fatalf("envelope=%s", encoded)
		}
	}
	enSchema, err := Schema([]string{"F001", "F002"})
	if err != nil {
		t.Fatal(err)
	}
	jaSchema, err := Schema([]string{"F001", "F002"})
	if err != nil || !bytesEqual(enSchema, jaSchema) {
		t.Fatalf("schema changed across language: %v", err)
	}
}

func TestValidateAcceptsLegalGroupingWithoutReordering(t *testing.T) {
	candidate := []byte(`{"schema_version":1,"commits":[{"type":"test","scope":"planner","breaking":false,"summary":"cover validation","file_ids":["F002"]},{"type":"feat","scope":"planner","breaking":true,"summary":"generate plans","file_ids":["F001"]}]}`)
	plan, violations := Validate(candidate, []string{"F001", "F002"}, SensitiveValues{})
	if len(violations) != 0 {
		t.Fatalf("violations=%v", violations)
	}
	if plan.Commits[0].FileIDs[0] != "F002" || plan.Commits[1].FileIDs[0] != "F001" || plan.Commits[1].Subject() != "feat(planner)!: generate plans" {
		t.Fatalf("plan was rewritten: %#v", plan)
	}
}

func TestValidateRejectsEverySchemaAndAssignmentClass(t *testing.T) {
	tests := map[string]string{
		"invalid JSON":          `not-json`,
		"unknown body":          `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":false,"summary":"s","body":"no","file_ids":["F001","F002"]}]}`,
		"missing breaking":      `{"schema_version":1,"commits":[{"type":"fix","scope":"x","summary":"s","file_ids":["F001","F002"]}]}`,
		"null breaking":         `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":null,"summary":"s","file_ids":["F001","F002"]}]}`,
		"invalid type":          `{"schema_version":1,"commits":[{"type":"feature","scope":"x","breaking":false,"summary":"s","file_ids":["F001","F002"]}]}`,
		"empty scope":           `{"schema_version":1,"commits":[{"type":"fix","scope":" ","breaking":false,"summary":"s","file_ids":["F001","F002"]}]}`,
		"multiline scope":       "{\"schema_version\":1,\"commits\":[{\"type\":\"fix\",\"scope\":\"x\\ny\",\"breaking\":false,\"summary\":\"s\",\"file_ids\":[\"F001\",\"F002\"]}]}",
		"multiline summary":     "{\"schema_version\":1,\"commits\":[{\"type\":\"fix\",\"scope\":\"x\",\"breaking\":false,\"summary\":\"one\\ntwo\",\"file_ids\":[\"F001\",\"F002\"]}]}",
		"missing":               `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":false,"summary":"s","file_ids":["F001"]}]}`,
		"duplicate same commit": `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":false,"summary":"s","file_ids":["F001","F001","F002"]}]}`,
		"duplicate commits":     `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":false,"summary":"s","file_ids":["F001"]},{"type":"test","scope":"x","breaking":false,"summary":"t","file_ids":["F001","F002"]}]}`,
		"out of range":          `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":false,"summary":"s","file_ids":["F001","F002","F003"]}]}`,
		"extra JSON":            `{"schema_version":1,"commits":[]} {}`,
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, violations := Validate([]byte(candidate), []string{"F001", "F002"}, SensitiveValues{}); len(violations) == 0 {
				t.Fatal("invalid candidate was accepted")
			}
		})
	}
}

func TestValidateRequiresBooleanBreaking(t *testing.T) {
	for name, candidate := range map[string]string{
		"missing": `{"schema_version":1,"commits":[{"type":"fix","scope":"x","summary":"s","file_ids":["F001","F002"]}]}`,
		"null":    `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":null,"summary":"s","file_ids":["F001","F002"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, violations := Validate([]byte(candidate), []string{"F001", "F002"}, SensitiveValues{})
			if !containsViolation(violations, InvalidSchema) {
				t.Fatalf("violations=%v", violations)
			}
		})
	}
}

func TestValidateRejectsSensitiveValueAfterJSONUnescaping(t *testing.T) {
	sensitive := ExtractSensitiveValues([]byte(`token = "secret123"`))
	candidate := []byte(`{"schema_version":1,"commits":[{"type":"fix","scope":"planner","breaking":false,"summary":"\u0073ecret123","file_ids":["F001","F002"]}]}`)
	_, violations := Validate(candidate, []string{"F001", "F002"}, sensitive)
	if !containsViolation(violations, SensitiveOutput) {
		t.Fatalf("violations=%v", violations)
	}
}

func TestValidateDoesNotRejectPlanMetadataForSensitiveJSONScalars(t *testing.T) {
	candidate := []byte(`{"schema_version":1,"commits":[{"type":"fix","scope":"planner","breaking":false,"summary":"valid plan","file_ids":["F001","F002"]}]}`)
	for name, source := range map[string]string{
		"number":         `{"token":1}`,
		"boolean":        `{"token":false}`,
		"string number":  `{"token":"1"}`,
		"string boolean": `{"token":"false"}`,
	} {
		t.Run(name, func(t *testing.T) {
			sensitive := ExtractSensitiveValues([]byte(source))
			_, violations := Validate(candidate, []string{"F001", "F002"}, sensitive)
			if containsViolation(violations, SensitiveOutput) {
				t.Fatalf("metadata caused a sensitive match: %v", violations)
			}
		})
	}
}

func TestValidateStillRejectsSensitiveJSONScalarsInSummary(t *testing.T) {
	for name, test := range map[string]struct {
		source  string
		summary string
	}{
		"number":         {source: `{"token":1}`, summary: "plan v1"},
		"boolean":        {source: `{"token":false}`, summary: "false setting"},
		"string number":  {source: `{"token":"1"}`, summary: "plan v1"},
		"string boolean": {source: `{"token":"false"}`, summary: "false setting"},
	} {
		t.Run(name, func(t *testing.T) {
			sensitive := ExtractSensitiveValues([]byte(test.source))
			candidate := []byte(`{"schema_version":1,"commits":[{"type":"fix","scope":"planner","breaking":true,"summary":"` + test.summary + `","file_ids":["F001","F002"]}]}`)
			_, violations := Validate(candidate, []string{"F001", "F002"}, sensitive)
			if !containsViolation(violations, SensitiveOutput) {
				t.Fatalf("summary scalar was not detected: %v", violations)
			}
		})
	}
}

func TestSensitiveExtractionCoversSpecifiedFormsAndExactBytes(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\nABC123\n-----END PRIVATE KEY-----"
	contents := []byte(strings.Join([]string{
		`{"client_secret":"JsonSecret","nested":{"token":42}}`,
		`password = "TomlSecret"`,
		`Authorization: Bearer BearerSecret`,
		`eyJhbGciOiJIUzI1NiJ9.payload.signature`,
		`github_pat_ProviderSecret`,
		`https://user:UriSecret@example.invalid/path`,
		privateKey,
	}, "\n"))
	sensitive := ExtractSensitiveValues(contents)
	for _, value := range []string{"JsonSecret", "42", "TomlSecret", "BearerSecret", "eyJhbGciOiJIUzI1NiJ9.payload.signature", "github_pat_ProviderSecret", "user:UriSecret", "UriSecret", privateKey} {
		if !sensitive.Contains([]byte("prefix " + value + " suffix")) {
			t.Fatalf("did not extract specified value form %q", value)
		}
	}
	if sensitive.Contains([]byte("jsonsecret")) || sensitive.Contains([]byte("encoded-value")) {
		t.Fatal("matching was not exact and case-sensitive")
	}
	encoded, err := json.Marshal(sensitive)
	if err != nil || strings.Contains(string(encoded), "JsonSecret") {
		t.Fatalf("sensitive values are serializable: %s, %v", encoded, err)
	}
}

func TestSensitiveExtractionHandlesInlineCommentsWithoutIncludingThem(t *testing.T) {
	sensitive := ExtractSensitiveValues([]byte(strings.Join([]string{
		`token = "QuotedSecret" # production token`,
		`password: PlainSecret # local only`,
		`client_secret = "Hash#Inside" # comment`,
	}, "\n")))
	for _, value := range []string{"QuotedSecret", "PlainSecret", "Hash#Inside"} {
		if !sensitive.Contains([]byte("summary " + value)) {
			t.Fatalf("did not extract %q", value)
		}
	}
	for _, value := range sensitive.values {
		if string(value) == `"QuotedSecret" # production token` {
			t.Fatal("inline comment was included in the extracted value")
		}
	}
}

func TestGeneratorRepairsOnceAndSharesTransportRetryBudget(t *testing.T) {
	secret := "DoNotExpose"
	invalid := `{"schema_version":1,"commits":[{"type":"fix","scope":"x","breaking":false,"summary":"` + secret + `","file_ids":["F001"]}]}`
	valid := `{"schema_version":1,"commits":[{"type":"fix","scope":"planner","breaking":false,"summary":"repair generated plan","file_ids":["F001","F002"]}]}`
	client := &scriptedChat{steps: []chatStep{
		{content: invalid},
		{err: context.DeadlineExceeded},
		{content: valid},
	}}
	prepared := preparedInput(t, English)
	result, err := (Generator{Client: client}).Generate(context.Background(), prepared, English, ExtractSensitiveValues([]byte("token="+secret)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Calls != 3 || !result.Repaired || len(client.messages) != 3 {
		t.Fatalf("result=%#v calls=%d", result, len(client.messages))
	}
	var repair struct {
		Violations []Violation `json:"violations"`
		Candidate  string      `json:"untrusted_candidate"`
	}
	if err := json.Unmarshal([]byte(client.messages[1][1].Content), &repair); err != nil {
		t.Fatal(err)
	}
	if !containsViolation(repair.Violations, SensitiveOutput) || repair.Candidate != invalid {
		t.Fatalf("repair payload=%#v", repair)
	}
	for _, violation := range repair.Violations {
		if strings.Contains(string(violation), secret) {
			t.Fatal("repair reason exposed a sensitive value")
		}
	}
}

func TestGeneratorStopsAtThreeCallsAndNeverRepairsTwice(t *testing.T) {
	invalid := `{"schema_version":1,"commits":[]}`
	for name, test := range map[string]struct {
		steps []chatStep
		calls int
	}{
		"shared retry exhausted": {[]chatStep{{err: context.DeadlineExceeded}, {content: invalid}, {err: context.DeadlineExceeded}, {content: validPlan()}}, 3},
		"repair remains invalid": {[]chatStep{{content: invalid}, {content: invalid}, {content: validPlan()}}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			client := &scriptedChat{steps: test.steps}
			_, err := (Generator{Client: client}).Generate(context.Background(), preparedInput(t, English), English, SensitiveValues{})
			if exitcode.Code(err) != exitcode.LLM || len(client.messages) != test.calls {
				t.Fatalf("error=%v code=%d calls=%d", err, exitcode.Code(err), len(client.messages))
			}
		})
	}
}

func TestGeneratorRejectsPreparedPromptWithDifferentLanguage(t *testing.T) {
	client := &scriptedChat{steps: []chatStep{{content: validPlan()}}}
	_, err := (Generator{Client: client}).Generate(context.Background(), preparedInput(t, English), Japanese, SensitiveValues{})
	if err == nil || len(client.messages) != 0 {
		t.Fatalf("error=%v calls=%d", err, len(client.messages))
	}
}

func TestGeneratorKeepsSchemaForJapaneseSummary(t *testing.T) {
	client := &scriptedChat{steps: []chatStep{{content: `{"schema_version":1,"commits":[{"type":"feat","scope":"planner","breaking":false,"summary":"コミット計画を生成する","file_ids":["F001","F002"]}]}`}}}
	result, err := (Generator{Client: client}).Generate(context.Background(), preparedInput(t, Japanese), Japanese, SensitiveValues{})
	if err != nil || result.Plan.SchemaVersion != SchemaVersion || result.Plan.Commits[0].Summary != "コミット計画を生成する" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestGeneratorAggregatesOnlyNonContentTelemetry(t *testing.T) {
	client := &scriptedChat{steps: []chatStep{{
		content: validPlan(), model: "model:tag", loadDuration: 2,
		promptEvalDuration: 3, evalDuration: 5, promptEvalCount: 7, evalCount: 11,
	}}}
	result, err := (Generator{Client: client}).Generate(context.Background(), preparedInput(t, English), English, SensitiveValues{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Telemetry != (Telemetry{Model: "model:tag", LoadDuration: 2, PromptEvalDuration: 3, EvalDuration: 5, PromptEvalCount: 7, EvalCount: 11}) {
		t.Fatalf("telemetry=%+v", result.Telemetry)
	}
}

func TestGeneratorKeepsTelemetryWhenRepairRequestFails(t *testing.T) {
	client := &scriptedChat{steps: []chatStep{
		{
			content: `{"schema_version":1,"commits":[]}`, model: "model:tag",
			loadDuration: 10, promptEvalDuration: 20, evalDuration: 30,
			promptEvalCount: 4, evalCount: 5,
		},
		{err: errors.New("repair transport failed")},
	}}
	result, err := (Generator{Client: client}).Generate(context.Background(), preparedInput(t, English), English, SensitiveValues{})
	if exitcode.Code(err) != exitcode.LLM || result.Calls != 2 {
		t.Fatalf("error=%v code=%d calls=%d", err, exitcode.Code(err), result.Calls)
	}
	if result.Telemetry != (Telemetry{Model: "model:tag", LoadDuration: 10, PromptEvalDuration: 20, EvalDuration: 30, PromptEvalCount: 4, EvalCount: 5}) {
		t.Fatalf("successful call telemetry was discarded: %+v", result.Telemetry)
	}
}

type chatStep struct {
	content            string
	err                error
	model              string
	loadDuration       int64
	promptEvalDuration int64
	evalDuration       int64
	promptEvalCount    int
	evalCount          int
}

type scriptedChat struct {
	steps    []chatStep
	messages [][]ollama.Message
}

func (client *scriptedChat) Chat(_ context.Context, messages []ollama.Message, schema json.RawMessage) (ollama.ChatResponse, error) {
	client.messages = append(client.messages, append([]ollama.Message(nil), messages...))
	if !json.Valid(schema) || len(client.steps) == 0 {
		return ollama.ChatResponse{}, errors.New("invalid fixture call")
	}
	step := client.steps[0]
	client.steps = client.steps[1:]
	return ollama.ChatResponse{
		Model: step.model, Content: step.content, LoadDuration: step.loadDuration,
		PromptEvalDuration: step.promptEvalDuration, EvalDuration: step.evalDuration,
		PromptEvalCount: step.promptEvalCount, EvalCount: step.evalCount,
	}, step.err
}

func preparedInput(t *testing.T, language Language) contextinput.Prepared {
	t.Helper()
	document := planningDocument()
	prepared, err := contextinput.Prepare(context.Background(), document, contextinput.BudgetConfig{Context: "8k", MaxContextTokens: contextinput.Context32K}, Renderer(language), nil)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func planningDocument() contextinput.Document {
	one, two := "one.go", "asset.bin"
	return contextinput.Document{
		SchemaVersion: contextinput.SchemaVersion,
		Repository:    contextinput.Repository{Head: "head", Branch: "main", IndexIdentity: "index"},
		Files: []contextinput.File{
			{ID: "F001", Status: "M", NewPath: &one, ChangeHash: "hash-1", WorktreeKind: "file", Mode: syntax.ModeStructural, Evidence: []syntax.Evidence{{Kind: "function_declaration", Name: "changed"}}},
			{ID: "F002", Status: "?", NewPath: &two, ChangeHash: "hash-2", WorktreeKind: "file", Opaque: true, Mode: syntax.ModeMetadataOnly},
		},
	}
}

func validPlan() string {
	return `{"schema_version":1,"commits":[{"type":"fix","scope":"planner","breaking":false,"summary":"valid plan","file_ids":["F001","F002"]}]}`
}

func bytesEqual(left, right []byte) bool { return string(left) == string(right) }
