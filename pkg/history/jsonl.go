package history

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
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
	promptRunes         = 160
)

var errInvalidRecord = native.ErrInvalidRecord

type transcript struct {
	match       Match
	recognized  bool
	hits        []bool
	excluded    bool
	lastMessage string
}

func newDatabaseTranscript(c Conversation) transcript {
	return transcript{
		match:       Match{Conversation: c, Excerpts: nil, MatchingParts: 0, ResumeCommand: nil, RegistryStates: nil},
		recognized:  true,
		hits:        nil,
		excluded:    false,
		lastMessage: "",
	}
}

// absorb folds the text matches of a child history into t. Conversation
// metadata stays t's own, so filters judge the session its parent recorded.
func (t *transcript) absorb(child *transcript, excerpts int) {
	t.excluded = t.excluded || child.excluded
	t.match.MatchingParts += child.match.MatchingParts
	if t.hits == nil && child.hits != nil {
		t.hits = make([]bool, len(child.hits))
	}
	for i, hit := range child.hits {
		t.hits[i] = t.hits[i] || hit
	}
	room := max(excerpts-len(t.match.Excerpts), 0)
	t.match.Excerpts = append(t.match.Excerpts, child.match.Excerpts[:min(room, len(child.match.Excerpts))]...)
}

// qualifies reports whether every term matched and no exclusion did.
func (t *transcript) qualifies(terms int) bool {
	if t.excluded || t.match.MatchingParts == 0 || len(t.hits) < terms {
		return false
	}
	return !slices.Contains(t.hits, false)
}

func (s *search) scanJSONL(ctx context.Context, source Source, path string, file *os.File) *transcript {
	info, err := file.Stat()
	if err != nil {
		s.issue(source, path, err)
		return nil
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
	lines, records, complete := s.readJSONLLines(ctx, source, path, reader, &t, lines)
	if ctx.Err() == nil && !t.identified() && (records > 0 || start > 0) {
		s.issue(source, path, errUnknownFormat)
	}
	if s.writer != nil {
		s.writer.checkpoint.Conversation = t.match.Conversation
		s.writer.checkpoint.Recognized = t.recognized
		s.writer.checkpoint.Complete = complete && ctx.Err() == nil
		s.writer.checkpoint.Lines = lines
		s.writer.checkpoint.Size = info.Size()
		s.writer.checkpoint.Digest = hex.EncodeToString(s.writer.hash.Sum(nil))
	}
	return &t
}

func (s *search) readJSONLLines(ctx context.Context, source Source, path string, reader *bufio.Reader, t *transcript, lines int) (int, int, bool) {
	complete := false
	records := 0
	readerSpec := catalog.TranscriptFor(source.Harness)
	decoder := s.decoder(source, t)
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
		records++
		if readerSpec.FastRecord != nil && readerSpec.FastRecord(ctx, decoder, data, line) {
			continue
		}
		var r native.Record
		if json.Unmarshal(data, &r) != nil {
			s.recordProblem(source, path, fmt.Errorf("line %d: %w", line, errInvalidRecord))
			continue
		}
		if readerSpec.Record != nil {
			readerSpec.Record(ctx, decoder, r, line)
		}
	}
	return lines, records, complete
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
func (s *search) capture(t *transcript, role, body, id string, line int, timestamp time.Time) {
	c := &t.match.Conversation
	extendTimes(c, timestamp)
	t.countMessage(role, id, line)
	if c.Prompt == "" && role == "user" {
		c.Prompt = clip(cleanPrompt(body), promptRunes)
	}
	if s.writer != nil {
		s.writer.append(Excerpt{Role: role, Text: body, Spans: nil, MessageID: id, Line: line, Timestamp: timestamp})
		return
	}
	s.matchText(t, role, body, id, line, timestamp)
}

func extendTimes(c *Conversation, timestamp time.Time) {
	if timestamp.IsZero() {
		return
	}
	if c.CreatedAt.IsZero() || timestamp.Before(c.CreatedAt) {
		c.CreatedAt = timestamp
	}
	if timestamp.After(c.UpdatedAt) {
		c.UpdatedAt = timestamp
	}
}

func (t *transcript) countMessage(role, id string, line int) {
	if role != "user" && role != "assistant" {
		return
	}
	if key := id + "\x00" + strconv.Itoa(line); key != t.lastMessage || (id == "" && line == 0) {
		t.lastMessage = key
		t.match.Conversation.Messages++
	}
}

// matchText records the first match of any term in one message part and builds
// a bounded window around it. Tool parts are skipped unless the query includes
// tools: this is the only place that decision is made for matching, so
// conversation metadata stays independent of IncludeTools.
func (s *search) matchText(t *transcript, role, body, id string, line int, timestamp time.Time) {
	if s.skipsPart(t, role) {
		return
	}
	normalized := body
	if !s.query.CaseSensitive {
		normalized = fold(body)
	}
	for _, m := range s.exclude {
		if m.matches(body, normalized) {
			t.excluded = true
			return
		}
	}
	if t.hits == nil {
		t.hits = make([]bool, len(s.terms))
	}
	anchor, length, matched := t.recordHits(s.terms, body, normalized)
	if !matched {
		return
	}
	t.match.MatchingParts++
	if len(t.match.Excerpts) >= s.query.Excerpts {
		return
	}
	text := excerptWindow(body, anchor, length)
	t.match.Excerpts = append(t.match.Excerpts, Excerpt{Role: role, Text: text, Spans: spans(text, s.terms), MessageID: id, Line: line, Timestamp: timestamp})
}

func (s *search) skipsPart(t *transcript, role string) bool {
	if s.mode != modeSearch || t.excluded {
		return true
	}
	if role == "tool" && !s.query.IncludeTools {
		return true
	}
	return s.query.Role != "" && s.query.Role != role
}

func (t *transcript) recordHits(terms []matcher, body, normalized string) (int, int, bool) {
	anchor, length, matched := 0, 0, false
	for i, m := range terms {
		offset, runes, ok := m.first(body, normalized)
		if !ok {
			continue
		}
		t.hits[i] = true
		if !matched || offset < anchor {
			anchor, length = offset, runes
		}
		matched = true
	}
	return anchor, length, matched
}

func excerptWindow(body string, anchor, needleRunes int) string {
	start := max(0, anchor-excerptRunes/4)
	end := start + max(excerptRunes, needleRunes)
	startByte, endByte := len(body), len(body)
	runeIndex := 0
	for byteOffset := range body {
		if runeIndex == start {
			startByte = byteOffset
		}
		if runeIndex == end {
			endByte = byteOffset
			break
		}
		runeIndex++
	}
	if anchor > runeIndex {
		return excerptWindow(body, runeIndex, needleRunes)
	}
	excerpt := body[startByte:endByte]
	if !utf8.ValidString(excerpt) {
		excerpt = string([]rune(excerpt))
	}
	prefix, suffix := "", ""
	if startByte > 0 {
		prefix = "…"
	}
	if endByte < len(body) {
		suffix = "…"
	}
	return prefix + excerpt + suffix
}

func clip(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func (s *search) handleLineReadError(source Source, path string, line int, err error) bool {
	if errors.Is(err, errRecordSize) {
		s.recordProblem(source, path, fmt.Errorf("line %d: %w", line, err))
		return true
	}
	s.issue(source, path, fmt.Errorf("line %d: %w", line, err))
	return false
}
