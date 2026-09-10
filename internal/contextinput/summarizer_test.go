package contextinput

import (
	"context"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

func TestHierarchicalSummarizerCompressesFileHunkAndChunk(t *testing.T) {
	raw := "diff --git a/main.txt b/main.txt\nindex 123..456 100644\n--- a/main.txt\n+++ b/main.txt\n@@ -1,3 +1,3 @@\n unchanged\n-old\n+new\n"
	document := rawDocument(raw)
	summarizer := NewHierarchicalSummarizer()

	fileSummary, err := summarizer.Summarize(context.Background(), SummaryFile, document)
	if err != nil {
		t.Fatal(err)
	}
	if fileSummary.Files[0].RawDiff != "" || strings.Contains(fileSummary.Files[0].Summary, "diff --git") || !strings.Contains(fileSummary.Files[0].Summary, " unchanged") {
		t.Fatalf("file summary=%q", fileSummary.Files[0].Summary)
	}

	hunkSummary, err := summarizer.Summarize(context.Background(), SummaryHunk, fileSummary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hunkSummary.Files[0].Summary, " unchanged") || !strings.Contains(hunkSummary.Files[0].Summary, "-old") || !strings.Contains(hunkSummary.Files[0].Summary, "+new") {
		t.Fatalf("hunk summary=%q", hunkSummary.Files[0].Summary)
	}

	chunkSummary, err := summarizer.Summarize(context.Background(), SummaryChunk, hunkSummary)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hunk: @@ -1,3 +1,3 @@", "chunk 1:", "additions=1", "deletions=1", "sha256=", "added: new", "removed: old"} {
		if !strings.Contains(chunkSummary.Files[0].Summary, want) {
			t.Fatalf("chunk summary=%q missing %q", chunkSummary.Files[0].Summary, want)
		}
	}
}

func TestHierarchicalSummarizerLeavesStructuralAndMetadataOnlyFilesUntouched(t *testing.T) {
	document := testDocument()
	document.Files = append(document.Files, File{ID: "F002", ChangeHash: "hash-2", Mode: syntax.ModeMetadataOnly, Opaque: true})
	want := cloneDocument(document)
	got, err := NewHierarchicalSummarizer().Summarize(context.Background(), SummaryFile, document)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePreserved(want, got); err != nil {
		t.Fatal(err)
	}
	if got.Files[0].Summary != "" || got.Files[1].Summary != "" {
		t.Fatalf("document=%#v", got)
	}
}

func TestHierarchicalSummarizerUsesBoundedUTF8Excerpts(t *testing.T) {
	document := rawDocument("@@ -1 +1 @@\n-古い値です\n+新しい値です\n")
	summarizer := HierarchicalSummarizer{ChunkLines: 2, ExcerptLines: 1, ExcerptBytes: 5}
	got, err := summarizer.Summarize(context.Background(), SummaryChunk, document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Files[0].Summary, "...") || strings.ToValidUTF8(got.Files[0].Summary, "") != got.Files[0].Summary {
		t.Fatalf("summary=%q", got.Files[0].Summary)
	}
}

func TestPrepareUsesStandardSummarizerWhenOversized(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("diff --git a/main.txt b/main.txt\n--- a/main.txt\n+++ b/main.txt\n@@ -1,3000 +1,2 @@\n")
	for range 3000 {
		raw.WriteString(" unchanged context that is removed at hunk stage\n")
	}
	raw.WriteString("-old\n+new\n")

	prepared, err := Prepare(
		context.Background(), rawDocument(raw.String()),
		BudgetConfig{Context: "8k", MaxContextTokens: Context32K}, JSONRenderer, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SummaryStage != SummaryHunk || prepared.SummaryCount != 2 || prepared.Document.Files[0].RawDiff != "" {
		t.Fatalf("prepared=%#v", prepared)
	}
}

func TestPrepareStandardSummarizerFallsThroughToChunkStage(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("@@ -1,400 +1,400 @@\n")
	for range 400 {
		raw.WriteString("+a changed line with enough repeated detail to keep the hunk above the configured context budget 1234567890\n")
	}

	prepared, err := Prepare(
		context.Background(), rawDocument(raw.String()),
		BudgetConfig{Context: "8k", MaxContextTokens: Context32K}, JSONRenderer, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SummaryStage != SummaryChunk || prepared.SummaryCount != 3 || !strings.Contains(prepared.Document.Files[0].Summary, "chunk 1:") {
		t.Fatalf("stage=%s count=%d summary_bytes=%d", prepared.SummaryStage, prepared.SummaryCount, len(prepared.Document.Files[0].Summary))
	}
}

func rawDocument(raw string) Document {
	path := "main.txt"
	return Document{
		SchemaVersion: SchemaVersion,
		Repository:    Repository{Head: "head", Branch: "main", IndexIdentity: "index"},
		Files: []File{{
			ID: "F001", Status: "M", NewPath: &path, WorktreeKind: "file", Language: "Text",
			ChangeHash: "hash", Mode: syntax.ModeRawDiff, RawDiff: raw,
		}},
	}
}
