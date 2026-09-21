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
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zigai/aht/pkg/registry"
)

const (
	excerptRunes        = 240
	scannerInitialBytes = 4096
	titleRunes          = 80
)

var errInvalidRecord = errors.New("invalid JSON history record")

type record map[string]json.RawMessage

type textPart struct{ role, text string }

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
	if source.Harness == registry.HarnessKimiCode {
		t.match.Conversation.SessionID = filepath.Base(filepath.Dir(path))
		t.match.Conversation.CWD = s.kimiDirs[filepath.Base(filepath.Dir(filepath.Dir(path)))]
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
		var r record
		if json.Unmarshal(data, &r) != nil {
			s.issue(source, path, fmt.Errorf("line %d: %w", line, errInvalidRecord))
			continue
		}
		s.readRecord(ctx, t, r, line)
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

func (s *search) readRecord(ctx context.Context, t *transcript, r record, line int) {
	switch t.match.Conversation.Harness {
	case registry.HarnessCodex:
		s.codexRecord(ctx, t, r, line)
	case registry.HarnessClaude:
		s.claudeRecord(ctx, t, r, line)
	case registry.HarnessCopilot:
		s.copilotRecord(ctx, t, r, line)
	case registry.HarnessKimiCode:
		if str(r, "role") != "" {
			t.recognized = true
			s.message(ctx, t, r, str(r, "id"), line, parseTime(r["timestamp"]))
		}
	case registry.HarnessPi, registry.HarnessOmp, registry.HarnessOpenClaw:
		s.treeRecord(ctx, t, r, line)
	case registry.HarnessCursor, registry.HarnessCline, registry.HarnessGrok, registry.HarnessGoose, registry.HarnessOpenCode, registry.HarnessAgy, registry.HarnessKilo, registry.HarnessDroid, registry.HarnessHermes:
		return // These harnesses have document/SQLite readers or no supported reader.
	}
}

func (s *search) treeRecord(ctx context.Context, t *transcript, r record, line int) {
	switch str(r, "type") {
	case "session":
		t.recognized = true
		t.match.Conversation.SessionID = str(r, "id")
		t.match.Conversation.CWD = str(r, "cwd")
		if value := parseTime(r["timestamp"]); !value.IsZero() {
			t.match.Conversation.CreatedAt = value
		}
		if title := str(r, "title"); title != "" {
			t.match.Conversation.Title = title
		}
	case "session_info":
		t.match.Conversation.Title = str(r, "name")
	case "title", "title_change":
		if title := str(r, "title"); title != "" {
			t.match.Conversation.Title = title
		}
	case "message":
		s.message(ctx, t, obj(r, "message"), str(r, "id"), line, parseTime(r["timestamp"]))
	}
}

func (s *search) codexRecord(ctx context.Context, t *transcript, r record, line int) {
	payload := obj(r, "payload")
	switch str(r, "type") {
	case "session_meta":
		t.recognized = true
		t.match.Conversation.SessionID = str(payload, "id")
		t.match.Conversation.CWD = str(payload, "cwd")
		t.match.Conversation.CreatedAt = parseTime(payload["timestamp"])
	case "response_item":
		timestamp := parseTime(r["timestamp"])
		switch str(payload, "type") {
		case "message":
			if str(payload, "channel") != "analysis" {
				s.message(ctx, t, payload, str(payload, "id"), line, timestamp)
			}
		case "function_call", "custom_tool_call":
			s.capture(ctx, t, "tool", str(payload, "name")+" "+firstString(payload, "arguments", "input"), str(payload, "call_id"), line, timestamp)
		case "function_call_output", "custom_tool_call_output":
			s.capture(ctx, t, "tool", contentText(payload["output"]), str(payload, "call_id"), line, timestamp)
		}
	}
}

func (s *search) claudeRecord(ctx context.Context, t *transcript, r record, line int) {
	kind := str(r, "type")
	if kind == "custom-title" {
		t.match.Conversation.Title = str(r, "customTitle")
		return
	}
	if kind != "user" && kind != "assistant" {
		return
	}
	t.recognized = true
	c := &t.match.Conversation
	if id := str(r, "sessionId"); id != "" {
		c.SessionID = id
	}
	if cwd := str(r, "cwd"); cwd != "" {
		c.CWD = cwd
	}
	s.message(ctx, t, obj(r, "message"), str(r, "uuid"), line, parseTime(r["timestamp"]))
}

func (s *search) copilotRecord(ctx context.Context, t *transcript, r record, line int) {
	data := obj(r, "data")
	timestamp := parseTime(r["timestamp"])
	switch str(r, "type") {
	case "session.start":
		t.recognized = true
		t.match.Conversation.SessionID = str(data, "sessionId")
		t.match.Conversation.CWD = str(obj(data, "context"), "cwd")
		t.match.Conversation.ProjectRoot = str(obj(data, "context"), "gitRoot")
		t.match.Conversation.CreatedAt = parseTime(data["startTime"])
	case "session.title_changed":
		t.match.Conversation.Title = str(data, "title")
	case "user.message":
		s.capture(ctx, t, "user", str(data, "content"), str(r, "id"), line, timestamp)
	case "assistant.message":
		s.capture(ctx, t, "assistant", str(data, "content"), str(r, "id"), line, timestamp)
		s.capture(ctx, t, "tool", string(data["toolRequests"]), str(r, "id"), line, timestamp)
	case "tool.execution_complete":
		s.capture(ctx, t, "tool", firstString(obj(data, "result"), "detailedContent", "content"), str(r, "id"), line, timestamp)
	}
}

// message captures every recognized message, including tool content, so
// conversation metadata does not depend on IncludeTools. The index writer
// decides whether tool parts are stored and matchText decides whether they are
// searched; readers never filter on the query.
func (s *search) message(ctx context.Context, t *transcript, r record, id string, line int, timestamp time.Time) {
	role, ok := recognizedRole(str(r, "role"))
	if !ok {
		return
	}
	for _, part := range messageParts(r["content"], role, true) {
		s.capture(ctx, t, part.role, part.text, id, line, timestamp)
	}
	if len(r["tool_calls"]) > 0 {
		s.capture(ctx, t, "tool", string(r["tool_calls"]), id, line, timestamp)
	}
}

// recognizedRole maps a native message role to the searchable role set. Reasoning
// and system messages stay unsearchable in every reader.
func recognizedRole(role string) (string, bool) {
	switch role {
	case "user", "assistant":
		return role, true
	case "toolResult", "tool":
		return "tool", true
	default:
		return "", false
	}
}

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

func messageParts(raw json.RawMessage, role string, tools bool) []textPart {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return []textPart{{role: role, text: string(raw[1 : len(raw)-1])}}
	}
	var body string
	if json.Unmarshal(raw, &body) == nil {
		return []textPart{{role: role, text: body}}
	}
	var blocks []record
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return messageBlockParts(blocks, role, tools)
}

func messageBlockParts(blocks []record, role string, tools bool) []textPart {
	var text strings.Builder
	var toolText strings.Builder
	for _, block := range blocks {
		kind := str(block, "type")
		if kind == "text" || kind == "input_text" || kind == "output_text" {
			text.WriteString(str(block, "text"))
			text.WriteByte('\n')
			continue
		}
		if tools {
			if body := toolContent(block); body != "" {
				toolText.WriteString(body)
				toolText.WriteByte('\n')
			}
		}
	}
	var parts []textPart
	if text.Len() > 0 {
		parts = append(parts, textPart{role: role, text: strings.TrimSuffix(text.String(), "\n")})
	}
	if toolText.Len() > 0 {
		parts = append(parts, textPart{role: "tool", text: strings.TrimSuffix(toolText.String(), "\n")})
	}
	return parts
}

func contentText(raw json.RawMessage) string {
	parts := messageParts(raw, "tool", false)
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.text)
	}
	return text.String()
}

func str(r record, key string) string {
	raw := r[key]
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1])
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func obj(r record, key string) record {
	var value record
	_ = json.Unmarshal(r[key], &value)
	return value
}

func firstString(r record, keys ...string) string {
	for _, key := range keys {
		if value := str(r, key); value != "" {
			return value
		}
	}
	return ""
}

func parseTime(raw json.RawMessage) time.Time {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return nativeTime(string(raw[1 : len(raw)-1]))
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return nativeTime(text)
	}
	number, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || !(number > 0) {
		return time.Time{}
	}
	const millisThreshold = 1e11
	const millisPerSecond = 1000
	if number >= millisThreshold {
		number /= millisPerSecond
	}
	const latestUnixSecond = 253402300799 // Last second representable by RFC3339.
	if number > latestUnixSecond {
		return time.Time{}
	}
	seconds := int64(number)
	return time.Unix(seconds, int64((number-float64(seconds))*float64(time.Second))).UTC()
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
		return nativeTime(string(raw))
	}
	var text string
	if json.Unmarshal(rest[:end+2], &text) == nil {
		return nativeTime(text)
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

func toolContent(block record) string {
	switch str(block, "type") {
	case "tool":
		return toolBlockText(block)
	case "toolRequest":
		call := obj(obj(block, "toolCall"), "value")
		return str(call, "name") + " " + string(call["arguments"])
	case "toolResponse":
		result := obj(block, "toolResult")
		if str(result, "status") == "error" {
			return str(result, "error")
		}
		return contentText(obj(result, "value")["content"])
	case "tool-call":
		return str(block, "toolName") + " " + string(block["input"])
	case "tool-result":
		return string(block["output"])
	case "tool_result":
		return contentText(block["content"])
	case "tool_use", "toolCall":
		return str(block, "name") + " " + string(block["input"]) + string(block["arguments"])
	default:
		return ""
	}
}

func nativeTime(text string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if value, err := time.Parse(layout, text); err == nil {
			return value
		}
	}
	return time.Time{}
}
