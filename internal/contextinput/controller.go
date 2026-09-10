package contextinput

import (
	"context"
	"errors"
	"fmt"

	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

type SummaryStage string

const (
	SummaryNone  SummaryStage = "none"
	SummaryFile  SummaryStage = "file"
	SummaryHunk  SummaryStage = "hunk"
	SummaryChunk SummaryStage = "chunk"
)

type Summarizer interface {
	Summarize(context.Context, SummaryStage, Document) (Document, error)
}

type Prepared struct {
	Document     Document     `json:"document"`
	Prompt       []byte       `json:"-"`
	Budget       Budget       `json:"budget"`
	SummaryStage SummaryStage `json:"summary_stage"`
	SummaryCount int          `json:"summary_count"`
}

// Prepare renders and measures the exact final prompt. Oversized input is
// summarized in the specified file -> hunk -> chunk order, with the complete
// identity and structural-evidence set checked after every pass.
func Prepare(ctx context.Context, document Document, config BudgetConfig, render Renderer, summarizer Summarizer) (Prepared, error) {
	if render == nil {
		return Prepared{}, errors.New("prompt renderer is required")
	}
	original := cloneDocument(document)
	current := cloneDocument(document)
	for attempt, stage := range []SummaryStage{SummaryNone, SummaryFile, SummaryHunk, SummaryChunk} {
		if stage != SummaryNone {
			if summarizer == nil {
				return Prepared{}, ErrTooLarge
			}
			next, err := summarizer.Summarize(ctx, stage, cloneDocument(current))
			if err != nil {
				return Prepared{}, fmt.Errorf("%s summary failed: %w", stage, err)
			}
			if err := ValidatePreserved(original, next); err != nil {
				return Prepared{}, fmt.Errorf("%s summary is incomplete: %w", stage, err)
			}
			current = cloneDocument(next)
		}
		prompt, err := render(cloneDocument(current))
		if err != nil {
			return Prepared{}, fmt.Errorf("cannot render planning input: %w", err)
		}
		budget, err := SelectContext(prompt, len(original.Files), config)
		if err == nil {
			return Prepared{
				Document: cloneDocument(current), Prompt: append([]byte(nil), prompt...), Budget: budget,
				SummaryStage: stage, SummaryCount: attempt,
			}, nil
		}
		if !errors.Is(err, ErrTooLarge) {
			return Prepared{}, err
		}
	}
	return Prepared{}, ErrTooLarge
}

func cloneDocument(document Document) Document {
	clone := document
	clone.Files = make([]File, len(document.Files))
	for i, file := range document.Files {
		clone.Files[i] = file
		clone.Files[i].OldPath = cloneString(file.OldPath)
		clone.Files[i].NewPath = cloneString(file.NewPath)
		clone.Files[i].Evidence = append([]syntax.Evidence(nil), file.Evidence...)
	}
	return clone
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
