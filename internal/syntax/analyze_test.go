package syntax

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
)

func TestAnalyzeAllSupportedLanguages(t *testing.T) {
	tests := []struct{ language, source, kind string }{
		{"go", "package p\nfunc changed() {}\n", "function_declaration"},
		{"javascript", "function changed() {}\n", "function_declaration"},
		{"jsx", "const view = <div className=\"x\" />\n", "jsx_self_closing_element"},
		{"typescript", "function changed(): number { return 1 }\n", "function_declaration"},
		{"tsx", "const view = <div />\n", "jsx_self_closing_element"},
		{"python", "def changed():\n    return 1\n", "function_definition"},
		{"rust", "fn changed() -> i32 { 1 }\n", "function_item"},
		{"html", "<main data-kind=\"x\"></main>\n", "element"},
		{"css", ".changed { color: red; }\n", "class_selector"},
	}
	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			result := Analyze(Input{Language: test.language, Content: []byte(test.source), Hunks: []Hunk{{StartLine: 1, EndLine: 999}}})
			if result.Mode != ModeStructural || len(result.Evidence) == 0 {
				t.Fatalf("result = %#v", result)
			}
			found := false
			for _, evidence := range result.Evidence {
				if evidence.Kind == test.kind {
					found = true
				}
			}
			if !found {
				t.Fatalf("kind %q not found in %#v", test.kind, result.Evidence)
			}
		})
	}
}

func TestAnalyzeFallsBackLocally(t *testing.T) {
	for name, input := range map[string]Input{
		"unsupported": {Language: "Ruby", Content: []byte("def x; end"), Hunks: []Hunk{{1, 1}}},
		"malformed":   {Language: "Go", Content: []byte("func ("), Hunks: []Hunk{{1, 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			result := Analyze(input)
			if result.Mode == ModeStructural || len(result.Evidence) != 0 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestAnalyzeEvidenceIsDeterministicAndSyntactic(t *testing.T) {
	input := Input{Language: "Go", Content: []byte("package p\nimport \"fmt\"\nfunc changed() { fmt.Println(1) }\n"), Hunks: []Hunk{{StartLine: 2, EndLine: 3}}}
	one, two := Analyze(input), Analyze(input)
	if len(one.Evidence) == 0 || len(one.Evidence) != len(two.Evidence) {
		t.Fatalf("evidence = %#v / %#v", one, two)
	}
	for i := range one.Evidence {
		if one.Evidence[i] != two.Evidence[i] {
			t.Fatalf("non-deterministic evidence")
		}
		if one.Evidence[i].Name == "fmt" {
			t.Fatalf("source text leaked into evidence: %#v", one.Evidence[i])
		}
	}
}

func TestAnalyzeFocusedStructuralEvidence(t *testing.T) {
	goResult := Analyze(Input{Language: "Go", Content: []byte("package p\nimport \"fmt\"\nfunc changed() { fmt.Println(1) }\n"), Hunks: []Hunk{{StartLine: 2, EndLine: 3}}})
	assertEvidence(t, goResult, func(e Evidence) bool { return e.Kind == "function_declaration" && e.Name == "changed" })
	assertEvidence(t, goResult, func(e Evidence) bool { return e.Role == "import" })
	assertEvidence(t, goResult, func(e Evidence) bool { return e.Role == "call" && e.EnclosingDeclaration == "changed" })

	jsResult := Analyze(Input{Language: "JavaScript", Content: []byte("export const save = () => api.call()\n"), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	assertEvidence(t, jsResult, func(e Evidence) bool { return e.Role == "export" })
	assertEvidence(t, jsResult, func(e Evidence) bool { return e.Role == "call" && e.EnclosingDeclaration == "save" })

	htmlResult := Analyze(Input{Language: "HTML", Content: []byte("<main data-kind=\"x\"></main>\n"), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	assertEvidence(t, htmlResult, func(e Evidence) bool { return e.Kind == "tag_name" && e.Name == "main" })
	assertEvidence(t, htmlResult, func(e Evidence) bool { return e.Kind == "attribute" })

	cssResult := Analyze(Input{Language: "CSS", Content: []byte(".changed { color: red; }\n"), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	assertEvidence(t, cssResult, func(e Evidence) bool { return e.Kind == "class_selector" })
	assertEvidence(t, cssResult, func(e Evidence) bool { return e.Kind == "property_name" || e.Kind == "property" })
}

func assertEvidence(t *testing.T, result Result, match func(Evidence) bool) {
	t.Helper()
	for _, evidence := range result.Evidence {
		if match(evidence) {
			return
		}
	}
	t.Fatalf("expected evidence in %#v", result.Evidence)
}

func TestAnalyzeHunkBoundariesAndFallbackSemantics(t *testing.T) {
	input := Input{Language: "Go", Content: []byte("package p\nfunc first() {}\nfunc second() {}\n"), Hunks: []Hunk{{StartLine: 2, EndLine: 2}}}
	result := Analyze(input)
	for _, evidence := range result.Evidence {
		if evidence.StartLine > 2 || evidence.EndLine < 2 {
			t.Fatalf("adjacent node leaked into hunk: %#v", evidence)
		}
	}
	if got := Analyze(Input{Language: "Go", Content: input.Content, Hunks: []Hunk{{StartLine: 0, EndLine: 0}}}); got.Mode != ModeRawDiff {
		t.Fatalf("invalid hunk result = %#v", got)
	}
	empty := Analyze(Input{Language: "Go", Content: input.Content, Hunks: []Hunk{{StartLine: 2, EndLine: 0}}})
	if empty.Mode != ModeRawDiff || len(empty.Evidence) != 0 {
		t.Fatalf("empty hunk result = %#v", empty)
	}
	duplicates := Analyze(Input{Language: "Go", Content: input.Content, Hunks: []Hunk{{StartLine: 2, EndLine: 2}, {StartLine: 2, EndLine: 2}}})
	if len(duplicates.Evidence) != len(result.Evidence) {
		t.Fatalf("duplicate hunk changed evidence: %#v / %#v", result, duplicates)
	}
	unsupported := Analyze(Input{Language: "Ruby", Content: []byte("def x; end"), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	if unsupported.Mode != ModeRawDiff {
		t.Fatalf("unsupported result = %#v", unsupported)
	}
	malformed := Analyze(Input{Language: "Go", Content: []byte("func ("), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	if malformed.Mode != ModeRawDiff {
		t.Fatalf("malformed result = %#v", malformed)
	}
}

func TestAnalyzeChangePreservesGitMetadataAndSeparatesFallbackModes(t *testing.T) {
	malformed := []byte("func (")
	malformedID := contentID(malformed)
	base := gitstate.Change{ID: "F001", Status: "M", Language: "Go", ChangeHash: "hash", WorktreeKind: "file", WorktreeID: &malformedID}
	raw := "@@ -1 +1 @@\n-old\n+new\n"

	text, err := AnalyzeChange(ChangeInput{Change: base, Content: malformed, RawDiff: raw, Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if text.Mode != ModeRawDiff || text.Change.ID != "F001" || text.RawDiff != raw {
		t.Fatalf("text fallback = %#v", text)
	}

	for name, change := range map[string]gitstate.Change{
		"binary":  {ID: "F002", Language: "Go", WorktreeKind: "file", Binary: true},
		"opaque":  {ID: "F003", Language: "Go", WorktreeKind: "file", Opaque: true},
		"symlink": {ID: "F004", Language: "Go", WorktreeKind: "symlink"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := AnalyzeChange(ChangeInput{Change: change, Content: []byte("package secret"), RawDiff: raw, Hunks: []Hunk{{1, 1}}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Mode != ModeMetadataOnly || len(result.RawDiff) != 0 || len(result.Evidence) != 0 {
				t.Fatalf("metadata-only result = %#v", result)
			}
		})
	}
}

func TestAnalyzeChangeUsesApprovedSensitiveStateAndRejectsStaleContent(t *testing.T) {
	content := []byte("package p\nfunc changed() {}\n")
	identity := contentID(content)
	change := gitstate.Change{ID: "F001", Language: "Go", WorktreeKind: "file", WorktreeID: &identity, Sensitive: true}

	result, err := AnalyzeChange(ChangeInput{Change: change, Content: content, Hunks: []Hunk{{1, 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != ModeStructural {
		t.Fatalf("approved sensitive result = %#v", result)
	}

	if _, err := AnalyzeChange(ChangeInput{Change: change, Content: []byte("package changed\n"), Hunks: []Hunk{{1, 1}}}); err == nil {
		t.Fatal("stale content was accepted")
	}
	missingIdentity := change
	missingIdentity.WorktreeID = nil
	if _, err := AnalyzeChange(ChangeInput{Change: missingIdentity, Content: content, Hunks: []Hunk{{1, 2}}}); err == nil {
		t.Fatal("regular file without identity was accepted")
	}
}

func contentID(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
