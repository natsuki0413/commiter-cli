// Package metrics records a fixed set of non-content execution measurements.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const SchemaVersion = 1

type Phase string

const (
	GitPreprocessing Phase = "git_preprocessing"
	SyntaxAnalysis   Phase = "syntax_analysis"
	ModelLoad        Phase = "model_load"
	PromptEvaluation Phase = "prompt_evaluation"
	Generation       Phase = "generation"
	Summarization    Phase = "summarization"
	Verification     Phase = "verification"
	Git              Phase = "git"
	Push             Phase = "push"
)

type Durations struct {
	GitPreprocessing *int64 `json:"git_preprocessing_ns,omitempty"`
	SyntaxAnalysis   *int64 `json:"syntax_analysis_ns,omitempty"`
	ModelLoad        *int64 `json:"model_load_ns,omitempty"`
	PromptEvaluation *int64 `json:"prompt_evaluation_ns,omitempty"`
	Generation       *int64 `json:"generation_ns,omitempty"`
	Summarization    *int64 `json:"summarization_ns,omitempty"`
	Verification     *int64 `json:"verification_ns,omitempty"`
	Git              *int64 `json:"git_ns,omitempty"`
	Push             *int64 `json:"push_ns,omitempty"`
}

type Counts struct {
	Files          int   `json:"files"`
	Lines          int   `json:"lines"`
	Bytes          int64 `json:"bytes"`
	SyntaxSuccess  int   `json:"syntax_success"`
	SyntaxFallback int   `json:"syntax_fallback"`
	Summaries      int   `json:"summaries"`
}

type Record struct {
	SchemaVersion int       `json:"schema_version"`
	RecordedAt    time.Time `json:"recorded_at"`
	Durations     Durations `json:"durations_ns"`
	Counts        Counts    `json:"counts"`
	Model         string    `json:"model,omitempty"`
	Context       string    `json:"context,omitempty"`
	Exit          string    `json:"exit"`
}

type Recorder struct {
	mu     sync.Mutex
	record Record
}

func New() *Recorder {
	return &Recorder{record: Record{SchemaVersion: SchemaVersion, RecordedAt: time.Now().UTC()}}
}

func (r *Recorder) AddDuration(phase Phase, duration time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target := phasePointer(&r.record.Durations, phase)
	if target == nil {
		return
	}
	value := duration.Nanoseconds()
	if *target != nil {
		value += **target
	}
	*target = &value
}

func (r *Recorder) SetPlanning(model, contextStage string, files, lines int, bytes int64, syntaxSuccess, syntaxFallback, summaries int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Model = model
	r.record.Context = contextStage
	r.record.Counts = Counts{
		Files: files, Lines: lines, Bytes: bytes, SyntaxSuccess: syntaxSuccess,
		SyntaxFallback: syntaxFallback, Summaries: summaries,
	}
}

func (r *Recorder) SetAnalysis(model string, files, lines int, bytes int64, syntaxSuccess, syntaxFallback int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Model = model
	r.record.Counts.Files = files
	r.record.Counts.Lines = lines
	r.record.Counts.Bytes = bytes
	r.record.Counts.SyntaxSuccess = syntaxSuccess
	r.record.Counts.SyntaxFallback = syntaxFallback
}

func (r *Recorder) SetContext(model, contextStage string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if model != "" {
		r.record.Model = model
	}
	r.record.Context = contextStage
}

func (r *Recorder) AddSummaries(count int) {
	if r == nil || count <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Counts.Summaries += count
}

func (r *Recorder) Finish(classification string) Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.Exit = classification
	return clone(r.record)
}

func Write(stateDir string, record Record) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("cannot create metrics state directory")
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return fmt.Errorf("cannot secure metrics state directory")
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("cannot encode metrics")
	}
	line = append(line, '\n')
	file, err := os.OpenFile(filepath.Join(stateDir, "metrics.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("cannot open metrics file")
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("cannot secure metrics file")
	}
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("cannot append metrics")
	}
	return nil
}

type recorderKey struct{}

func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func FromContext(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(recorderKey{}).(*Recorder)
	return recorder
}

func phasePointer(durations *Durations, phase Phase) **int64 {
	switch phase {
	case GitPreprocessing:
		return &durations.GitPreprocessing
	case SyntaxAnalysis:
		return &durations.SyntaxAnalysis
	case ModelLoad:
		return &durations.ModelLoad
	case PromptEvaluation:
		return &durations.PromptEvaluation
	case Generation:
		return &durations.Generation
	case Summarization:
		return &durations.Summarization
	case Verification:
		return &durations.Verification
	case Git:
		return &durations.Git
	case Push:
		return &durations.Push
	default:
		return nil
	}
}

func clone(record Record) Record {
	encoded, _ := json.Marshal(record)
	var copied Record
	_ = json.Unmarshal(encoded, &copied)
	return copied
}
