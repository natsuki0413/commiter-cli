package syntax

import "testing"

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
			if !result.Supported || result.Fallback || len(result.Evidence) == 0 {
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
		"unsupported":          {Language: "Ruby", Content: []byte("def x; end"), Hunks: []Hunk{{1, 1}}},
		"malformed":            {Language: "Go", Content: []byte("func ("), Hunks: []Hunk{{1, 1}}},
		"binary":               {Language: "Go", Content: []byte("package p"), Binary: true, Hunks: []Hunk{{1, 1}}},
		"opaque":               {Language: "Go", Content: []byte("package p"), Opaque: true, Hunks: []Hunk{{1, 1}}},
		"unapproved-sensitive": {Language: "Go", Content: []byte("package p"), Sensitive: true, Hunks: []Hunk{{1, 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			result := Analyze(input)
			if !result.Fallback || len(result.Evidence) != 0 {
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
	if got := Analyze(Input{Language: "Go", Content: input.Content, Hunks: []Hunk{{StartLine: 0, EndLine: 0}}}); !got.Supported || !got.Fallback {
		t.Fatalf("invalid hunk result = %#v", got)
	}
	empty := Analyze(Input{Language: "Go", Content: input.Content, Hunks: []Hunk{{StartLine: 2, EndLine: 0}}})
	if !empty.Supported || empty.Fallback || len(empty.Evidence) != 0 {
		t.Fatalf("empty hunk result = %#v", empty)
	}
	duplicates := Analyze(Input{Language: "Go", Content: input.Content, Hunks: []Hunk{{StartLine: 2, EndLine: 2}, {StartLine: 2, EndLine: 2}}})
	if len(duplicates.Evidence) != len(result.Evidence) {
		t.Fatalf("duplicate hunk changed evidence: %#v / %#v", result, duplicates)
	}
	unsupported := Analyze(Input{Language: "Ruby", Content: []byte("def x; end"), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	if unsupported.Supported || !unsupported.Fallback {
		t.Fatalf("unsupported result = %#v", unsupported)
	}
	malformed := Analyze(Input{Language: "Go", Content: []byte("func ("), Hunks: []Hunk{{StartLine: 1, EndLine: 1}}})
	if !malformed.Supported || !malformed.Fallback {
		t.Fatalf("malformed result = %#v", malformed)
	}
}
