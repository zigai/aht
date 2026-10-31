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

var errCompressedArchive = errors.New(
	"compressed history archive is not supported; select an uncompressed native export",
)

// scanTranscript parses one transcript file and returns its conversation, or
// nil when the file could not be read.
func (s *search) scanTranscript(
	ctx context.Context,
	source Source,
	path string,
	file *os.File,
	buffer *fileIssueBuffer,
) *transcript {
	if strings.HasSuffix(path, ".zst") {
		buffer.issue(source, path, errCompressedArchive)
		return nil
	}

	if parse := catalog.TranscriptFor(source.Harness).Document; parse != nil {
		return s.scanDocument(ctx, source, path, file, parse, buffer)
	}

	return s.scanJSONL(ctx, source, path, file, buffer, false)
}

func (s *search) checkTranscriptFormat(
	ctx context.Context,
	source Source,
	path string,
	file *os.File,
	buffer *fileIssueBuffer,
) {
	if catalog.TranscriptFor(source.Harness).Document != nil {
		s.scanTranscript(ctx, source, path, file, buffer)
		return
	}

	s.scanJSONL(ctx, source, path, file, buffer, true)
}

func (s *search) scanDocument(
	ctx context.Context,
	source Source,
	path string,
	file *os.File,
	parse func(context.Context, *native.Decoder, []byte) error,
	buffer *fileIssueBuffer,
) *transcript {
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil {
		buffer.issue(source, path, fmt.Errorf("read document: %w", err))
		return nil
	}

	if len(data) > maxDocumentBytes {
		buffer.issue(source, path, errDocumentSize)
		return nil
	}

	var t transcript

	t.match.Conversation.Harness = source.Harness

	t.match.Conversation.Path = path
	if err := parse(ctx, s.decoder(source, &t, buffer), data); err != nil {
		buffer.issue(source, path, err)
		return nil
	}

	if c := &t.match.Conversation; c.CreatedAt.IsZero() && c.UpdatedAt.IsZero() {
		info, err := file.Stat()
		if err != nil {
			buffer.issue(source, path, err)
			return nil
		}

		c.UpdatedAt = info.ModTime()
	}

	return &t
}
