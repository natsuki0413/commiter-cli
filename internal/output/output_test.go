package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestEscapeUntrustedTerminalText(t *testing.T) {
	input := "path\n\x1b[31mred\x00日本語"
	got := Escape(input)

	for _, forbidden := range []string{"\n", "\x1b", "\x00"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("Escape() retained control bytes: %q", got)
		}
	}
	for _, expected := range []string{`\n`, `\x1b`, `\x00`, "日本語"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("Escape() = %q, want %q", got, expected)
		}
	}
}

func TestJSONErrorDoesNotWriteStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	New(&stdout, &stderr, true).Error("bad\nvalue", 2)

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"exit_code":2`) || strings.Contains(got, "bad\nvalue") {
		t.Fatalf("stdout = %q", got)
	}
}
