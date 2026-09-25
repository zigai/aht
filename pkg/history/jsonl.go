package history

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	native "github.com/zigai/aht/v2/internal/harness/transcript"
)

const (
	excerptRunes        = 240
	scannerInitialBytes = 4096
	titleRunes          = 80
)

var errInvalidRecord = native.ErrInvalidRecord

type transcript struct {
	match      Match
	recognized bool
}

func (s *search) scanJSONL(ctx context.Context, source Source, path string, file *os.File) {
	info, err := file.Stat()
	if err != nil {
		s.issue(source, path, err)
		return
	}

	var t transcript
	t.match.Conversation.Harness = source.Harness
	t.match.Conversation.Path = path
	if initialize := catalog.TranscriptFor(source.Harness).Initialize; initialize != nil {
		initialize(s.decoder(source, &t))
	}
	start, lines := int64(0), 0
	if s.writer != nil && s.writer.resume != nil {
		checkpoint := s.writer.resume
		t.match.Conversation = checkpoint.Conversation
		t.recognized = checkpoint.Recognized
		start, lines = checkpoint.Size, checkpoint.Lines
	}
	// A captured file size makes a finite snapshot even while a harness appends.
	input := io.LimitReader(file, info.Size()-start)
	if s.writer != nil {
		input = io.TeeReader(input, s.writer.hash)
	}
	reader := bufio.NewReaderSize(input, scannerInitialBytes)
	lines, complete := s.readJSONLLines(ctx, source, path, reader, &t, lines)
	if ctx.Err() == nil && !t.identified() {
		s.issue(source, path, errUnknownFormat)
	}
	s.add(ctx, t.match)
	if s.writer != nil {
		s.writer.checkpoint.Conversation = t.match.Conversation
		s.writer.checkpoint.Recognized = t.recognized
		s.writer.checkpoint.Complete = complete && ctx.Err() == nil
		s.writer.checkpoint.Lines = lines
		s.writer.checkpoint.Size = info.Size()
		s.writer.checkpoint.Digest = hex.EncodeToString(s.writer.hash.Sum(nil))
	}
}

func (s *search) readJSONLLines(ctx context.Context, source Source, path string, reader *bufio.Reader, t *transcript, lines int) (int, bool) {
	complete := false
	for line := lines + 1; ; line++ {
		data, readErr := readRecordLine(ctx, reader)
		if errors.Is(readErr, io.EOF) || ctx.Err() != nil {
			break
		}
		if readErr != nil {
			lines, complete = line, false
			if s.handleLineReadError(source, path, line, readErr) {
				continue
			}
			break
		}
		lines = line
		complete = len(data) > 0 && data[len(data)-1] == '\n'
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		if s.canQuickUpdate(t, data) {
			s.quickUpdateMetadata(t, data)
			continue
		}
		var r native.Record
		if json.Unmarshal(data, &r) != nil {
			s.issue(source, path, fmt.Errorf("line %d: %w", line, errInvalidRecord))
			continue
		}
		if parse := catalog.TranscriptFor(source.Harness).Record; parse != nil {
			parse(ctx, s.decoder(source, t), r, line)
		}
	}
	return lines, complete
}

func readRecordLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	chunk, err := reader.ReadSlice('\n')
	if err == nil {
		return chunk, nil
	}
	return readLongRecord(ctx, reader, chunk, err)
}

func readLongRecord(ctx context.Context, reader *bufio.Reader, first []byte, readErr error) ([]byte, error) {
	data := append([]byte(nil), first...)
	oversized := false
	err := readErr
	for errors.Is(err, bufio.ErrBufferFull) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("read history: %w", ctxErr)
		}
		var chunk []byte
		chunk, err = reader.ReadSlice('\n')
		if !oversized && len(data)+len(chunk) <= maxRecordBytes {
			data = append(data, chunk...)
		} else {
			oversized = true
			data = nil
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read history line: %w", err)
	}
	if oversized {
		return nil, errRecordSize
	}
	if len(data) > 0 {
		return data, nil
	}
	return nil, io.EOF
}

func (t *transcript) identified() bool { return t.recognized && t.match.Conversation.SessionID != "" }

// capture aggregates conversation metadata for every recognized message and then
// either appends it to the index writer or matches it. Metadata is
// mode-independent: IncludeTools filtering belongs to the writer and to
// matchText, never here.
func (s *search) capture(ctx context.Context, t *transcript, role, body, id string, line int, timestamp time.Time) {
	c := &t.match.Conversation
	if !timestamp.IsZero() {
		if c.CreatedAt.IsZero() || timestamp.Before(c.CreatedAt) {
			c.CreatedAt = timestamp
		}
		if timestamp.After(c.UpdatedAt) {
			c.UpdatedAt = timestamp
		}
	}
	if c.Title == "" && role == "user" {
		c.Title = clip(cleanPromptTitle(body), titleRunes)
	}
	if s.writer != nil {
		s.writer.append(ctx, Excerpt{Role: role, Text: body, MessageID: id, Line: line, Timestamp: timestamp})
		return
	}
	s.matchText(t, role, body, id, line, timestamp)
}

// matchText records the first match of the normalized needle in one message part
// and builds a bounded window around it. Tool parts are skipped unless the query
// includes tools: this is the only place that decision is made for matching, so
// conversation metadata stays independent of IncludeTools.
func (s *search) matchText(t *transcript, role, body, id string, line int, timestamp time.Time) {
	if role == "tool" && !s.query.IncludeTools {
		return
	}
	if s.query.Role != "" && s.query.Role != role {
		return
	}
	value := body
	if !s.query.CaseSensitive {
		value = fold(value)
	}
	offset := strings.Index(value, s.needle)
	if offset < 0 {
		return
	}
	t.match.MatchingParts++
	if len(t.match.Excerpts) >= maxExcerpts {
		return
	}
	// The match offset belongs to folded text. Folding can change the rune count
	// (ß folds to ss), so the window anchor is the unfolded rune index whose
	// folded prefix covers that offset; case-sensitive matching never folds.
	anchor := utf8.RuneCountInString(value[:offset])
	runes := []rune(body)
	if !s.query.CaseSensitive {
		anchor = foldRuneIndex(body, anchor)
	}
	anchor = min(max(anchor, 0), len(runes))
	start := max(0, anchor-excerptRunes/4)
	end := min(len(runes), start+max(excerptRunes, utf8.RuneCountInString(s.needle)))
	excerpt := string(runes[start:end])
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	t.match.Excerpts = append(t.match.Excerpts, Excerpt{Role: role, Text: excerpt, MessageID: id, Line: line, Timestamp: timestamp})
}

func clip(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func cleanPromptTitle(body string) string {
	text := strings.TrimSpace(body)
	if _, after, ok := strings.Cut(text, "</INSTRUCTIONS>"); ok {
		if trimmed := strings.TrimSpace(after); trimmed != "" {
			return trimmed
		}
	}
	if _, after, ok := strings.Cut(text, "<INSTRUCTIONS>"); ok {
		if trimmed := strings.TrimSpace(after); trimmed != "" {
			return trimmed
		}
	}
	if _, after, ok := strings.Cut(text, "</environment_context>"); ok {
		if trimmed := strings.TrimSpace(after); trimmed != "" {
			return trimmed
		}
	}
	if strings.HasPrefix(text, "# AGENTS.md instructions") {
		for line := range strings.SplitSeq(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "<") {
				return trimmed
			}
		}
	}
	return text
}

func isSessionHeader(data []byte) bool {
	return bytes.Contains(data, []byte(`"session"`)) ||
		bytes.Contains(data, []byte(`"session_meta"`)) ||
		bytes.Contains(data, []byte(`"session_info"`)) ||
		bytes.Contains(data, []byte(`"title"`)) ||
		bytes.Contains(data, []byte(`"custom-title"`)) ||
		bytes.Contains(data, []byte(`"sessionId"`)) ||
		bytes.Contains(data, []byte(`"cwd"`))
}

func (s *search) quickUpdateMetadata(t *transcript, data []byte) {
	if ts := extractLineTimestamp(data); !ts.IsZero() {
		c := &t.match.Conversation
		if c.CreatedAt.IsZero() || ts.Before(c.CreatedAt) {
			c.CreatedAt = ts
		}
		if ts.After(c.UpdatedAt) {
			c.UpdatedAt = ts
		}
	}
}

func extractLineTimestamp(data []byte) time.Time {
	_, after, ok := bytes.Cut(data, []byte(`"timestamp"`))
	if !ok {
		return time.Time{}
	}
	rest := bytes.TrimLeft(after, " \t:")
	if len(rest) == 0 {
		return time.Time{}
	}
	if rest[0] == '"' {
		return extractStringTimestamp(rest)
	}
	return extractNumericTimestamp(rest)
}

func extractStringTimestamp(rest []byte) time.Time {
	end := bytes.IndexByte(rest[1:], '"')
	if end < 0 {
		return time.Time{}
	}
	raw := rest[1 : end+1]
	if bytes.IndexByte(raw, '\\') < 0 {
		return native.NativeTime(string(raw))
	}
	var text string
	if json.Unmarshal(rest[:end+2], &text) == nil {
		return native.NativeTime(text)
	}
	return time.Time{}
}

func extractNumericTimestamp(rest []byte) time.Time {
	end := 0
	for end < len(rest) && (rest[end] >= '0' && rest[end] <= '9' || rest[end] == '.') {
		end++
	}
	if end == 0 {
		return time.Time{}
	}
	number, err := strconv.ParseFloat(string(rest[:end]), 64)
	if err != nil || !(number > 0) {
		return time.Time{}
	}
	const millisThreshold = 1e11
	const millisPerSecond = 1000
	if number >= millisThreshold {
		number /= millisPerSecond
	}
	const latestUnixSecond = 253402300799
	if number > latestUnixSecond {
		return time.Time{}
	}
	seconds := int64(number)
	return time.Unix(seconds, int64((number-float64(seconds))*float64(time.Second))).UTC()
}

func (s *search) handleLineReadError(source Source, path string, line int, err error) bool {
	s.issue(source, path, fmt.Errorf("line %d: %w", line, err))
	return errors.Is(err, errRecordSize)
}

func (s *search) canQuickUpdate(t *transcript, data []byte) bool {
	return s.writer == nil && !s.containsNeedle(data) && t.identified() && !isSessionHeader(data) && t.match.Conversation.Title != ""
}
