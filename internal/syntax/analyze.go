// Package syntax extracts deterministic, syntax-only evidence from changed text.
package syntax

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"unsafe"

	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	treesitter "github.com/tree-sitter/go-tree-sitter"
	treecss "github.com/tree-sitter/tree-sitter-css/bindings/go"
	treego "github.com/tree-sitter/tree-sitter-go/bindings/go"
	treehtml "github.com/tree-sitter/tree-sitter-html/bindings/go"
	treejavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	treepython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	treerust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	treetypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Hunk is a 1-based inclusive line range in the new file. A zero EndLine
// represents a deletion-only hunk and falls back to raw diff.
type Hunk struct{ StartLine, EndLine int }

// Input contains only data already approved for local analysis.
type Input struct {
	Language string
	Content  []byte
	Hunks    []Hunk
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

type Mode string

const (
	ModeStructural   Mode = "structural"
	ModeRawDiff      Mode = "raw_diff"
	ModeMetadataOnly Mode = "metadata_only"
)

type Result struct {
	Mode     Mode       `json:"mode"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

// ChangeInput joins the approved bytes and diff hunks to the immutable Git
// metadata collected by gitstate. RawDiff must contain only the changed hunks
// that are safe to send to the local model.
type ChangeInput struct {
	Change  gitstate.Change
	Content []byte
	RawDiff string
	Hunks   []Hunk
}

type ChangeResult struct {
	Change   gitstate.Change `json:"change"`
	Mode     Mode            `json:"mode"`
	RawDiff  string          `json:"raw_diff,omitempty"`
	Evidence []Evidence      `json:"evidence,omitempty"`
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

// Analyze parses text that the caller has already approved for local analysis.
// Unsupported or invalid text falls back to its raw diff at the Change boundary.
func Analyze(input Input) Result {
	factory, ok := factories[strings.ToLower(input.Language)]
	if !ok {
		return Result{Mode: ModeRawDiff}
	}
	parser := treesitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(treesitter.NewLanguage(factory())); err != nil {
		return Result{Mode: ModeRawDiff}
	}
	tree := parser.Parse(input.Content, nil)
	if tree == nil {
		return Result{Mode: ModeRawDiff}
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return Result{Mode: ModeRawDiff}
	}
	lines := lineOffsets(input.Content)
	result := Result{Mode: ModeStructural}
	for _, hunk := range input.Hunks {
		start, end, ok := hunkBytes(hunk, lines, len(input.Content))
		if !ok {
			return Result{Mode: ModeRawDiff}
		}
		if start == end {
			return Result{Mode: ModeRawDiff}
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
	if len(result.Evidence) == 0 {
		return Result{Mode: ModeRawDiff}
	}
	return result
}

// AnalyzeChange preserves the Issue #3 Git metadata and makes raw-diff versus
// metadata-only fallback impossible to confuse at the next pipeline boundary.
func AnalyzeChange(input ChangeInput) (ChangeResult, error) {
	if input.Change.Binary || input.Change.Opaque {
		return ChangeResult{Change: input.Change, Mode: ModeMetadataOnly}, nil
	}
	if input.Change.WorktreeKind == "deleted" {
		return ChangeResult{Change: input.Change, Mode: ModeRawDiff, RawDiff: input.RawDiff}, nil
	}
	if input.Change.WorktreeKind != "file" {
		return ChangeResult{Change: input.Change, Mode: ModeMetadataOnly}, nil
	}
	if input.Change.WorktreeID == nil {
		return ChangeResult{}, errors.New("regular file is missing its working-tree identity")
	}
	digest := sha256.Sum256(input.Content)
	if hex.EncodeToString(digest[:]) != *input.Change.WorktreeID {
		return ChangeResult{}, errors.New("content does not match the collected Git state")
	}
	analysis := Analyze(Input{
		Language: input.Change.Language,
		Content:  input.Content,
		Hunks:    input.Hunks,
	})
	result := ChangeResult{Change: input.Change, Mode: analysis.Mode, Evidence: analysis.Evidence}
	if analysis.Mode == ModeRawDiff {
		result.RawDiff = input.RawDiff
	}
	return result, nil
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
		if declarationName(node, source) != "" || current == nil {
			current = node
		}
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
	if kind == "variable_declarator" {
		return true
	}
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
