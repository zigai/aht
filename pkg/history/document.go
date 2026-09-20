package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/pkg/registry"
)

const maxDocumentBytes = 64 << 20

var errDocumentSize = errors.New("history document exceeds 64 MiB")

var errCompressedArchive = errors.New("compressed history archive is not supported; select an uncompressed native export")

func (s *search) scanTranscript(ctx context.Context, source Source, path string, file *os.File) {
	if s.index != nil {
		// Refresh failures reach the caller as source issues and as the sticky
		// error index.commit reports; only an unusable index must be retained
		// here so the search can fall back to a direct scan.
		if err := s.index.transcript(ctx, s, source, path, file); errors.Is(err, errIndexUnavailable) {
			s.indexErr = err
		}
		return
	}
	if strings.HasSuffix(path, ".zst") {
		s.issue(source, path, errCompressedArchive)
		return
	}
	if source.Harness == registry.HarnessCline {
		s.scanCline(ctx, source, path, file)
		return
	}
	s.scanJSONL(ctx, source, path, file)
}

func (s *search) scanCline(ctx context.Context, source Source, path string, file *os.File) {
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		s.issue(source, path, fmt.Errorf("read messages: %w", err))
		return
	}
	if len(data) > maxDocumentBytes {
		s.issue(source, path, errDocumentSize)
		return
	}
	var r record
	if json.Unmarshal(data, &r) != nil {
		s.issue(source, path, errInvalidRecord)
		return
	}
	var messages []record
	if str(r, "sessionId") == "" || json.Unmarshal(r["messages"], &messages) != nil {
		s.issue(source, path, errUnknownFormat)
		return
	}
	var t transcript
	t.recognized = true
	t.match.Conversation.Harness = source.Harness
	t.match.Conversation.Path = path
	t.match.Conversation.SessionID = str(r, "sessionId")
	t.match.Conversation.UpdatedAt = parseTime(r["updated_at"])
	s.clineMetadata(source, path, &t)
	for _, message := range messages {
		if ctx.Err() != nil {
			return
		}
		s.message(ctx, &t, message, str(message, "id"), 0, parseTime(message["timestamp"]))
	}
	s.add(ctx, t.match)
}

func (s *search) clineMetadata(source Source, path string, t *transcript) {
	// Only the sibling manifest is consulted; embedded messages_path values are
	// never followed and cannot redirect discovery outside the selected source.
	name := strings.TrimSuffix(filepath.Base(path), ".messages.json") + ".json"
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		s.issue(source, path, err)
		return
	}
	defer func() {
		if err := root.Close(); err != nil {
			s.issue(source, path, err)
		}
	}()
	file, err := root.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		s.issue(source, path, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			s.issue(source, path, err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		s.issue(source, path, err)
		return
	}
	if len(data) > maxRecordBytes {
		s.issue(source, path, errRecordSize)
		return
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		s.issue(source, path, errInvalidRecord)
		return
	}
	if str(r, "session_id") != t.match.Conversation.SessionID {
		return
	}
	t.match.Conversation.CWD = str(r, "cwd")
	t.match.Conversation.ProjectRoot = str(r, "workspace_root")
	t.match.Conversation.CreatedAt = parseTime(r["started_at"])
	t.match.Conversation.Title = str(obj(r, "metadata"), "title")
}
