package history

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zigai/aht/internal/harness/catalog"
	native "github.com/zigai/aht/internal/harness/transcript"
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
	if parse := catalog.TranscriptFor(source.Harness).Document; parse != nil {
		s.scanDocument(ctx, source, path, file, parse)
		return
	}
	s.scanJSONL(ctx, source, path, file)
}

func (s *search) scanDocument(ctx context.Context, source Source, path string, file *os.File, parse func(context.Context, *native.Decoder, []byte) error) {
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		s.issue(source, path, fmt.Errorf("read document: %w", err))
		return
	}
	if len(data) > maxDocumentBytes {
		s.issue(source, path, errDocumentSize)
		return
	}
	var t transcript
	t.match.Conversation.Harness = source.Harness
	t.match.Conversation.Path = path
	if err := parse(ctx, s.decoder(source, &t), data); err != nil {
		s.issue(source, path, err)
		return
	}
	s.add(ctx, t.match)
}
