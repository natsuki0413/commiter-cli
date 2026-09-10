package contextinput

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/natsuki0413/commiter-cli/internal/syntax"
)

const (
	defaultChunkLines   = 64
	defaultExcerptLines = 2
	defaultExcerptBytes = 160
)

// HierarchicalSummarizer performs deterministic, local-only diff compression.
// It removes redundant file headers, then unchanged hunk context, and finally
// replaces fixed-size chunks with counts, a digest, and bounded changed-line
// excerpts. Git identities and structural evidence remain outside summaries.
type HierarchicalSummarizer struct {
	ChunkLines   int
	ExcerptLines int
	ExcerptBytes int
}

func NewHierarchicalSummarizer() HierarchicalSummarizer {
	return HierarchicalSummarizer{
		ChunkLines: defaultChunkLines, ExcerptLines: defaultExcerptLines, ExcerptBytes: defaultExcerptBytes,
	}
}

func (s HierarchicalSummarizer) Summarize(ctx context.Context, stage SummaryStage, document Document) (Document, error) {
	if err := s.validate(); err != nil {
		return Document{}, err
	}
	for i := range document.Files {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		file := &document.Files[i]
		if file.Mode != syntax.ModeRawDiff {
			continue
		}
		source := file.RawDiff
		if source == "" {
			source = file.Summary
		}
		if source == "" {
			return Document{}, fmt.Errorf("file %s has no raw diff to summarize", file.ID)
		}
		var summary string
		switch stage {
		case SummaryFile:
			summary = summarizeFile(source)
		case SummaryHunk:
			summary = summarizeHunks(source)
		case SummaryChunk:
			summary = s.summarizeChunks(source)
		default:
			return Document{}, fmt.Errorf("unsupported summary stage %q", stage)
		}
		file.RawDiff = ""
		file.Summary = summary
	}
	return document, nil
}

func (s HierarchicalSummarizer) validate() error {
	if s.ChunkLines <= 0 || s.ExcerptLines <= 0 || s.ExcerptBytes <= 0 {
		return errors.New("summary limits must be positive")
	}
	return nil
}

func summarizeFile(source string) string {
	lines := splitLines(source)
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if redundantFileHeader(line) {
			continue
		}
		kept = append(kept, line)
	}
	return joinSummary(kept, source)
}

func summarizeHunks(source string) string {
	lines := splitLines(source)
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, " ") {
			continue
		}
		kept = append(kept, line)
	}
	return joinSummary(kept, source)
}

func (s HierarchicalSummarizer) summarizeChunks(source string) string {
	lines := splitLines(source)
	var output []string
	activeHunk := ""
	for offset, number := 0, 1; offset < len(lines); offset, number = offset+s.ChunkLines, number+1 {
		end := offset + s.ChunkLines
		if end > len(lines) {
			end = len(lines)
		}
		chunk := lines[offset:end]
		hunks := make([]string, 0, 2)
		if activeHunk != "" {
			hunks = append(hunks, activeHunk)
		}
		for _, line := range chunk {
			if strings.HasPrefix(line, "@@") {
				activeHunk = line
				if len(hunks) == 0 || hunks[len(hunks)-1] != line {
					hunks = append(hunks, line)
				}
			}
		}
		for _, hunk := range hunks {
			output = append(output, "hunk: "+truncateUTF8(hunk, s.ExcerptBytes))
		}
		output = append(output, s.chunkSummary(number, chunk)...)
	}
	return joinSummary(output, source)
}

func (s HierarchicalSummarizer) chunkSummary(number int, lines []string) []string {
	additions, deletions, contextLines := 0, 0, 0
	excerpts := make([]string, 0, s.ExcerptLines*2)
	addedExcerpts, deletedExcerpts := 0, 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ "):
			additions++
			if addedExcerpts < s.ExcerptLines {
				excerpts = append(excerpts, "added: "+truncateUTF8(strings.TrimPrefix(line, "+"), s.ExcerptBytes))
				addedExcerpts++
			}
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- "):
			deletions++
			if deletedExcerpts < s.ExcerptLines {
				excerpts = append(excerpts, "removed: "+truncateUTF8(strings.TrimPrefix(line, "-"), s.ExcerptBytes))
				deletedExcerpts++
			}
		case strings.HasPrefix(line, " "):
			contextLines++
		}
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	header := fmt.Sprintf(
		"chunk %d: lines=%d additions=%d deletions=%d context=%d sha256=%s",
		number, len(lines), additions, deletions, contextLines, hex.EncodeToString(digest[:]),
	)
	return append([]string{header}, excerpts...)
}

func redundantFileHeader(line string) bool {
	return strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")
}

func splitLines(source string) []string {
	return strings.Split(strings.TrimSuffix(source, "\n"), "\n")
}

func joinSummary(lines []string, fallback string) string {
	summary := strings.Join(lines, "\n")
	if summary == "" {
		digest := sha256.Sum256([]byte(fallback))
		return "diff metadata only sha256=" + hex.EncodeToString(digest[:])
	}
	return summary
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "..."
}
