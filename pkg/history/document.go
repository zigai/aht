package history

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	native "github.com/zigai/aht/v2/internal/harness/transcript"
)

const maxDocumentBytes = 64 << 20

var errDocumentSize = errors.New("history document exceeds 64 MiB")

var errCompressedArchive = errors.New("compressed history archive is not supported; select an uncompressed native export")

// scanTranscript parses one transcript file and returns its conversation, or
// nil when the file could not be read.
func (s *search) scanTranscript(ctx context.Context, source Source, path string, file *os.File) *transcript {
	if strings.HasSuffix(path, ".zst") {
		s.issue(source, path, errCompressedArchive)
		return nil
	}
	if parse := catalog.TranscriptFor(source.Harness).Document; parse != nil {
		return s.scanDocument(ctx, source, path, file, parse)
	}
	return s.scanJSONL(ctx, source, path, file)
}

func (s *search) scanDocument(ctx context.Context, source Source, path string, file *os.File, parse func(context.Context, *native.Decoder, []byte) error) *transcript {
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		s.issue(source, path, fmt.Errorf("read document: %w", err))
		return nil
	}
	if len(data) > maxDocumentBytes {
		s.issue(source, path, errDocumentSize)
		return nil
	}
	var t transcript
	t.match.Conversation.Harness = source.Harness
	t.match.Conversation.Path = path
	if err := parse(ctx, s.decoder(source, &t), data); err != nil {
		s.issue(source, path, err)
		return nil
	}
	return &t
}
