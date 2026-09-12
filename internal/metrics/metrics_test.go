package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordHasFixedNonContentSchema(t *testing.T) {
	recorder := New()
	recorder.AddDuration(GitPreprocessing, time.Nanosecond)
	recorder.SetPlanning("model:tag", "8k", 2, 3, 4, 1, 1, 0)
	record := recorder.Finish("success")
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	for _, forbidden := range []string{"path", "diff", "prompt", "message", "feedback", "raw-secret"} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("record contains forbidden field or value %q: %s", forbidden, value)
		}
	}
	if record.Durations.GitPreprocessing == nil || record.Durations.Push != nil {
		t.Fatalf("executed and omitted phases were not distinguished: %+v", record.Durations)
	}
}

func TestWriteAppendsPrivateJSONL(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	record := New().Finish("safety_stop")
	if err := Write(stateDir, record); err != nil {
		t.Fatal(err)
	}
	if err := Write(stateDir, record); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "metrics.jsonl")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(content), "\n"); lines != 2 {
		t.Fatalf("lines=%d content=%q", lines, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}
