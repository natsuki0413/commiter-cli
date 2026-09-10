// Package syntax extracts deterministic, syntax-only evidence from changed text.
package syntax

import (
	"sort"
	"strings"
	"unsafe"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	treecss "github.com/tree-sitter/tree-sitter-css/bindings/go"
	treego "github.com/tree-sitter/tree-sitter-go/bindings/go"
	treehtml "github.com/tree-sitter/tree-sitter-html/bindings/go"
	treejavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	treepython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	treerust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	treetypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Hunk is a 1-based inclusive line range in the new file. An empty range is
// useful for an insertion at StartLine.
type Hunk struct{ StartLine, EndLine int }

// Input contains only data already approved for local analysis.
type Input struct {
	Language          string
	Content           []byte
	Hunks             []Hunk
	Binary            bool
	Opaque            bool
	Sensitive         bool
	SensitiveApproved bool
}

// Evidence is deliberately limited to syntactic facts; it contains no source
// text and no recommendation about grouping or change purpose.
type Evidence struct {
	Kind                 string `json:"kind"`
	Name                 string `json:"name,omitempty"`
	EnclosingDeclaration string `json:"enclosing_declaration,omitempty"`
	Role                 string `json:"role,omitempty"`
	StartLine            int    `json:"start_line"`
	EndLine              int    `json:"end_line"`
	StartByte            uint   `json:"start_byte"`
	EndByte              uint   `json:"end_byte"`
}

type Result struct {
	Supported bool       `json:"supported"`
	Fallback  bool       `json:"fallback"`
	Evidence  []Evidence `json:"evidence,omitempty"`
}

type languageFactory func() unsafe.Pointer

var factories = map[string]languageFactory{
	"go":         treego.Language,
	"javascript": treejavascript.Language,
	"jsx":        treejavascript.Language,
	"typescript": treetypescript.LanguageTypescript,
	"tsx":        treetypescript.LanguageTSX,
	"python":     treepython.Language,
	"rust":       treerust.Language,
	"html":       treehtml.Language,
	"css":        treecss.Language,
}

// Analyze parses one approved text file. Unsupported, opaque, binary, or
// unapproved sensitive input returns file-local fallback without an error.
func Analyze(input Input) Result {
	if input.Binary || input.Opaque || (input.Sensitive && !input.SensitiveApproved) {
		return Result{Fallback: true}
	}
	factory, ok := factories[strings.ToLower(input.Language)]
	if !ok {
		return Result{Fallback: true}
	}
	parser := treesitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(treesitter.NewLanguage(factory())); err != nil {
		return Result{Supported: true, Fallback: true}
	}
	tree := parser.Parse(input.Content, nil)
	if tree == nil {
		return Result{Supported: true, Fallback: true}
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return Result{Supported: true, Fallback: true}
	}
	lines := lineOffsets(input.Content)
	result := Result{Supported: true}
	for _, hunk := range input.Hunks {
		start, end, ok := hunkBytes(hunk, lines, len(input.Content))
		if !ok {
			return Result{Supported: true, Fallback: true}
		}
		if start == end {
			continue
		}
		collect(root, input.Content, start, end, nil, &result.Evidence)
	}
	sort.Slice(result.Evidence, func(i, j int) bool {
		a, b := result.Evidence[i], result.Evidence[j]
		if a.StartByte != b.StartByte {
			return a.StartByte < b.StartByte
		}
		if a.EndByte != b.EndByte {
			return a.EndByte < b.EndByte
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	result.Evidence = unique(result.Evidence)
	return result
}

func lineOffsets(source []byte) []int {
	offsets := []int{0}
	for i, b := range source {
		if b == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

func hunkBytes(h Hunk, lines []int, size int) (uint, uint, bool) {
	if h.StartLine < 1 || h.StartLine > len(lines) || h.EndLine < 0 || (h.EndLine != 0 && h.EndLine < h.StartLine) {
		return 0, 0, false
	}
	start := lines[h.StartLine-1]
	if h.EndLine == 0 {
		return uint(start), uint(start), true
	}
	end := size
	if h.EndLine < len(lines) {
		end = lines[h.EndLine]
	}
	return uint(start), uint(end), true
}

func collect(node *treesitter.Node, source []byte, start, end uint, declaration *treesitter.Node, out *[]Evidence) {
	if node == nil || node.EndByte() <= start || node.StartByte() >= end {
		return
	}
	kind := node.Kind()
	current := declaration
	if isDeclaration(kind) {
		current = node
	}
	if node.IsNamed() && kind != "source_file" && node.StartByte() <= end && node.EndByte() >= start {
		e := Evidence{Kind: kind, StartByte: node.StartByte(), EndByte: node.EndByte(), StartLine: int(node.StartPosition().Row) + 1, EndLine: int(node.EndPosition().Row) + 1}
		e.Name = nodeName(node, source)
		if current != nil && current != node {
			e.EnclosingDeclaration = declarationName(current, source)
		}
		e.Role = role(kind)
		*out = append(*out, e)
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		collect(node.NamedChild(i), source, start, end, current, out)
	}
}

func isDeclaration(kind string) bool {
	for _, part := range []string{"function", "method", "class", "struct", "interface", "module", "declaration", "definition"} {
		if strings.Contains(kind, part) {
			return true
		}
	}
	return false
}

func nodeName(node *treesitter.Node, source []byte) string {
	if name := node.ChildByFieldName("name"); name != nil {
		return name.Utf8Text(source)
	}
	if node.Kind() == "tag_name" || node.Kind() == "attribute_name" || node.Kind() == "property_name" || node.Kind() == "class_selector" {
		return node.Utf8Text(source)
	}
	return ""
}

func declarationName(node *treesitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return nodeName(node, source)
}

func role(kind string) string {
	switch {
	case strings.Contains(kind, "import"):
		return "import"
	case strings.Contains(kind, "export"):
		return "export"
	case strings.Contains(kind, "call") || strings.Contains(kind, "invocation"):
		return "call"
	default:
		return ""
	}
}

func unique(values []Evidence) []Evidence {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
