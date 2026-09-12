package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	runmetrics "github.com/natsuki0413/commiter-cli/internal/metrics"
	"github.com/natsuki0413/commiter-cli/internal/output"
)

func TestFinishMetricsDisplaysWithoutPersistingByDefault(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	recorder := runmetrics.New()
	recorder.AddDuration(runmetrics.GitPreprocessing, time.Nanosecond)
	recorder.SetPlanning("model:tag", "8k", 1, 2, 3, 1, 0, 0)
	var stdout, stderr bytes.Buffer

	code := finishMetrics(output.New(&stdout, &stderr, false), recorder, stateDir, false, exitcode.Safety)
	if code != exitcode.Safety {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "metrics.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("metrics file unexpectedly exists: %v", err)
	}
	value := stdout.String()
	for _, want := range []string{"git preprocessing:", "files=1", "exit: safety_stop"} {
		if !strings.Contains(value, want) {
			t.Fatalf("output=%q missing %q", value, want)
		}
	}
	if strings.Contains(value, "push:") {
		t.Fatalf("unexecuted push phase was displayed: %q", value)
	}
}

func TestFinishMetricsPersistsOnlyFixedSchemaAndKeepsJSONStdoutClean(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	recorder := runmetrics.New()
	recorder.AddDuration(runmetrics.Generation, time.Nanosecond)
	recorder.SetPlanning("model:tag", "16k", 2, 4, 8, 1, 1, 2)
	var stdout, stderr bytes.Buffer

	code := finishMetrics(output.New(&stdout, &stderr, true), recorder, stateDir, true, exitcode.Success)
	if code != exitcode.Success || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
	var displayed map[string]json.RawMessage
	if err := json.Unmarshal(stderr.Bytes(), &displayed); err != nil || displayed["metrics"] == nil {
		t.Fatalf("stderr=%q err=%v", stderr.String(), err)
	}
	content, err := os.ReadFile(filepath.Join(stateDir, "metrics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"path", "diff", "prompt", "message", "feedback", "raw-secret"} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("persisted metrics contains %q: %s", forbidden, content)
		}
	}
	for _, want := range []string{`"generation_ns":1`, `"summaries":2`, `"exit":"success"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content=%q missing %q", content, want)
		}
	}
}
