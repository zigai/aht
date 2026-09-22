// Package titlefile provides bounded JSONL reads shared by harness title readers.
package titlefile

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const maxRecordBytes = 64 << 10

var (
	errSourceNotRegular = errors.New("title source is not a regular file")
	// ErrRecordTooLarge means a JSONL record was skipped after its full line
	// was consumed. Scanners may continue with the next record.
	ErrRecordTooLarge = errors.New("title record exceeds 64 KiB")
)

// Open rejects directories and special files before reading metadata.
func Open(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect title source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errSourceNotRegular
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open title source: %w", err)
	}
	return file, nil
}

func ReadLine(ctx context.Context, reader *bufio.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read title line: %w", err)
	}
	chunk, err := reader.ReadSlice('\n')
	if len(chunk) > maxRecordBytes {
		return nil, skipOversizedLine(ctx, reader, err)
	}
	if err == nil || (errors.Is(err, io.EOF) && len(chunk) > 0) {
		return chunk, nil
	}
	if !errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("read title line: %w", err)
	}
	return readLongLine(ctx, reader, chunk)
}

func readLongLine(ctx context.Context, reader *bufio.Reader, first []byte) ([]byte, error) {
	line := append([]byte(nil), first...)
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > maxRecordBytes {
			return nil, skipOversizedLine(ctx, reader, err)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("read title line: %w", err)
			}
		case errors.Is(err, io.EOF) && len(line) > 0:
			return line, nil
		default:
			return nil, fmt.Errorf("read title line: %w", err)
		}
	}
}

func skipOversizedLine(ctx context.Context, reader *bufio.Reader, readErr error) error {
	for errors.Is(readErr, bufio.ErrBufferFull) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("discard oversized title line: %w", err)
		}
		_, readErr = reader.ReadSlice('\n')
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("discard oversized title line: %w", readErr)
	}
	return ErrRecordTooLarge
}
