package contextinput

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

type summarizeFunc func(context.Context, SummaryStage, Document) (Document, error)

func (f summarizeFunc) Summarize(ctx context.Context, stage SummaryStage, document Document) (Document, error) {
	return f(ctx, stage, document)
}

func TestPrepareUsesSmallestContextWithoutSummary(t *testing.T) {
	document := testDocument()
	prepared, err := Prepare(context.Background(), document, BudgetConfig{Context: "auto", MaxContextTokens: Context32K}, JSONRenderer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Budget.ContextTokens != Context8K || prepared.SummaryStage != SummaryNone || prepared.SummaryCount != 0 {
		t.Fatalf("prepared=%#v", prepared)
	}
	if len(prepared.Prompt) != prepared.Budget.PromptBytes {
		t.Fatalf("prompt=%d budget=%#v", len(prepared.Prompt), prepared.Budget)
	}
}

func TestPrepareSummarizesInFileHunkChunkOrder(t *testing.T) {
	document := testDocument()
	stages := []SummaryStage{}
	render := func(document Document) ([]byte, error) {
		switch document.Files[0].Summary {
		case "file":
			return make([]byte, Context16K), nil
		case "hunk":
			return make([]byte, Context8K), nil
		case "chunk":
			return []byte("fits"), nil
		default:
			return make([]byte, Context32K), nil
		}
	}
	summarizer := summarizeFunc(func(_ context.Context, stage SummaryStage, document Document) (Document, error) {
		stages = append(stages, stage)
		document.Files[0].RawDiff = ""
		document.Files[0].Summary = string(stage)
		return document, nil
	})
	prepared, err := Prepare(context.Background(), document, BudgetConfig{Context: "auto", MaxContextTokens: Context8K}, render, summarizer)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stages, []SummaryStage{SummaryFile, SummaryHunk, SummaryChunk}) || prepared.SummaryStage != SummaryChunk || prepared.SummaryCount != 3 {
		t.Fatalf("stages=%v prepared=%#v", stages, prepared)
	}
}

func TestPrepareRejectsIncompleteSummaryBeforeRenderingOrPlanning(t *testing.T) {
	document := testDocument()
	renders := 0
	render := func(Document) ([]byte, error) {
		renders++
		return make([]byte, Context32K), nil
	}
	summarizer := summarizeFunc(func(_ context.Context, _ SummaryStage, document Document) (Document, error) {
		document.Files[0].ChangeHash = "rewritten"
		return document, nil
	})
	_, err := Prepare(context.Background(), document, BudgetConfig{Context: "auto", MaxContextTokens: Context8K}, render, summarizer)
	if err == nil || !strings.Contains(err.Error(), "summary is incomplete") || renders != 1 {
		t.Fatalf("renders=%d error=%v", renders, err)
	}
}

func TestPrepareRejectsMissingOrDuplicateFileAndEvidenceChanges(t *testing.T) {
	for name, mutate := range map[string]func(Document) Document{
		"missing": func(document Document) Document {
			document.Files = document.Files[:0]
			return document
		},
		"duplicate": func(document Document) Document {
			document.Files = append(document.Files, document.Files[0])
			return document
		},
		"evidence": func(document Document) Document {
			document.Files[0].Evidence[0].Kind = "rewritten"
			return document
		},
		"path": func(document Document) Document {
			*document.Files[0].NewPath = "rewritten.go"
			return document
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := testDocument()
			summarizer := summarizeFunc(func(_ context.Context, _ SummaryStage, value Document) (Document, error) { return mutate(value), nil })
			_, err := Prepare(context.Background(), document, BudgetConfig{Context: "8k", MaxContextTokens: Context32K}, func(Document) ([]byte, error) {
				return make([]byte, Context8K), nil
			}, summarizer)
			if err == nil {
				t.Fatal("incomplete summary was accepted")
			}
		})
	}
}

func TestPrepareStopsAfterSummaryFailureOrFinalOverflow(t *testing.T) {
	document := testDocument()
	failure := errors.New("summary unavailable")
	_, err := Prepare(context.Background(), document, BudgetConfig{Context: "8k", MaxContextTokens: Context32K}, func(Document) ([]byte, error) {
		return make([]byte, Context8K), nil
	}, summarizeFunc(func(context.Context, SummaryStage, Document) (Document, error) {
		return Document{}, failure
	}))
	if !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}

	stages := 0
	_, err = Prepare(context.Background(), document, BudgetConfig{Context: "8k", MaxContextTokens: Context32K}, func(Document) ([]byte, error) {
		return make([]byte, Context8K), nil
	}, summarizeFunc(func(_ context.Context, _ SummaryStage, document Document) (Document, error) {
		stages++
		return document, nil
	}))
	if !errors.Is(err, ErrTooLarge) || stages != 3 {
		t.Fatalf("stages=%d error=%v", stages, err)
	}
}

func testDocument() Document {
	path := "main.go"
	return Document{
		SchemaVersion: SchemaVersion,
		Repository:    Repository{Head: "head", Branch: "main", IndexIdentity: "index"},
		Files: []File{{
			ID: "F001", Status: "M", NewPath: &path, Language: "Go", ChangeHash: "hash",
			Mode: syntax.ModeStructural, Evidence: []syntax.Evidence{{Kind: "function_declaration", Name: "changed"}},
		}},
	}
}
