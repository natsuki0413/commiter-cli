package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Printer struct {
	out  io.Writer
	err  io.Writer
	json bool
}

func New(out, errOut io.Writer, jsonMode bool) *Printer {
	return &Printer{out: out, err: errOut, json: jsonMode}
}

func (p *Printer) JSON() bool { return p.json }

func (p *Printer) Value(value any) error {
	if p.json {
		encoder := json.NewEncoder(p.out)
		encoder.SetEscapeHTML(true)
		return encoder.Encode(value)
	}
	_, err := fmt.Fprintln(p.out, Escape(fmt.Sprint(value)))
	return err
}

func (p *Printer) Lines(lines ...string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintln(p.out, Escape(line)); err != nil {
			return err
		}
	}
	return nil
}

func (p *Printer) Error(message string, code int) {
	if p.json {
		_ = json.NewEncoder(p.out).Encode(map[string]any{
			"error":     Escape(message),
			"exit_code": code,
		})
		return
	}
	_, _ = fmt.Fprintln(p.err, "error: "+Escape(message))
}

// Escape preserves printable Unicode while making every terminal control byte,
// newline, escape sequence, quote, and backslash visible instead of executable.
func Escape(value string) string {
	quoted := strconv.QuoteToGraphic(value)
	return strings.TrimSuffix(strings.TrimPrefix(quoted, `"`), `"`)
}
