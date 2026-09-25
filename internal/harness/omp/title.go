package omp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zigai/aht/v2/internal/harness/titlefile"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

// SessionTitles reads the current OMP title slot and checks its session header.
// Header-first files use the last title-change entry when present.
func (ompHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if identity.SessionID == "" || identity.SessionPath == "" {
			continue
		}
		title, err := readSessionTitle(ctx, identity.SessionID, identity.SessionPath)
		if err != nil {
			failures = append(failures, fmt.Errorf("read OMP session title: %w", err))
		} else {
			titles[i] = title
		}
	}
	return titles, errors.Join(failures...)
}

func readSessionTitle(ctx context.Context, sessionID, path string) (string, error) {
	file, err := titlefile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open OMP transcript: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only close cannot change the observed title.
	reader := bufio.NewReader(file)
	id, title, hasSlot, err := readSessionHeader(ctx, reader)
	if err != nil || id != sessionID {
		return "", err
	}
	if hasSlot {
		return title, nil
	}
	return scanLegacyTitle(ctx, reader, title)
}

func readSessionHeader(ctx context.Context, reader *bufio.Reader) (string, string, bool, error) {
	first, err := titlefile.ReadLine(ctx, reader)
	if errors.Is(err, io.EOF) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("read OMP session header: %w", err)
	}
	header, err := transcript.ParseTree(first)
	if err != nil {
		return "", "", false, fmt.Errorf("decode OMP session header: %w", err)
	}
	if header.Type == "title" {
		title := header.Title
		second, err := titlefile.ReadLine(ctx, reader)
		if err != nil {
			return "", "", false, fmt.Errorf("read OMP session header: %w", err)
		}
		header, err = transcript.ParseTree(second)
		if err != nil {
			return "", "", false, fmt.Errorf("decode OMP session header: %w", err)
		}
		if header.Type == "session" {
			return header.ID, title, true, nil
		}
		return "", "", false, nil
	}
	if header.Type != "session" {
		return "", "", false, nil
	}
	return header.ID, header.Title, false, nil
}

func scanLegacyTitle(ctx context.Context, reader *bufio.Reader, title string) (string, error) {
	for {
		line, err := titlefile.ReadLine(ctx, reader)
		if errors.Is(err, io.EOF) {
			return title, nil
		}
		if errors.Is(err, titlefile.ErrRecordTooLarge) {
			continue
		}
		if err != nil {
			return title, fmt.Errorf("scan OMP title changes: %w", err)
		}
		if !bytes.Contains(line, []byte(`"title_change"`)) {
			continue
		}
		entry, decodeErr := transcript.ParseTree(line)
		if decodeErr == nil && entry.Type == "title_change" {
			title = entry.Title
		}
	}
}
