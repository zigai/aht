package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zigai/aht/v2/internal/harness/titlefile"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (claudeHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
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
			failures = append(failures, fmt.Errorf("read Claude session title: %w", err))
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
		return "", fmt.Errorf("open Claude transcript: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return "", errors.Join(fmt.Errorf("inspect Claude transcript: %w", err), file.Close())
	}
	title, err := scanSessionTitle(ctx, bufio.NewReader(io.LimitReader(file, info.Size())), sessionID)
	if err = errors.Join(err, file.Close()); err != nil {
		return "", err
	}
	return title, nil
}

func scanSessionTitle(ctx context.Context, reader *bufio.Reader, sessionID string) (string, error) {
	var manual, generated string
	for {
		line, err := titlefile.ReadLine(ctx, reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, titlefile.ErrRecordTooLarge) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("scan Claude session titles: %w", err)
		}
		if !bytes.Contains(line, []byte(`"custom-title"`)) && !bytes.Contains(line, []byte(`"ai-title"`)) {
			continue
		}
		var record transcript.Record
		if json.Unmarshal(line, &record) != nil || transcript.Str(record, "sessionId") != sessionID {
			continue
		}
		kind, title, ok := titleRecord(record)
		if !ok {
			continue
		}
		if kind == "custom-title" {
			manual = title
		} else {
			generated = title
		}
	}
	if manual != "" {
		return manual, nil
	}
	return generated, nil
}
