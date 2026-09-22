package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zigai/aht/internal/harness/titlefile"
	"github.com/zigai/aht/pkg/registry"
)

// SessionTitles reads the last native session_info name from each Pi transcript.
func (piHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
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
			failures = append(failures, fmt.Errorf("read Pi session title: %w", err))
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
		return "", fmt.Errorf("open Pi transcript: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only close cannot change the observed name.
	reader := bufio.NewReader(file)
	valid, err := matchesSession(ctx, reader, sessionID)
	if err != nil || !valid {
		return "", err
	}
	return scanSessionName(ctx, reader)
}

func matchesSession(ctx context.Context, reader *bufio.Reader, sessionID string) (bool, error) {
	first, err := titlefile.ReadLine(ctx, reader)
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Pi session header: %w", err)
	}
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(first, &header); err != nil {
		return false, fmt.Errorf("decode Pi session header: %w", err)
	}
	return header.Type == "session" && header.ID == sessionID, nil
}

func scanSessionName(ctx context.Context, reader *bufio.Reader) (string, error) {
	title := ""
	for {
		line, err := titlefile.ReadLine(ctx, reader)
		if errors.Is(err, io.EOF) {
			return title, nil
		}
		if errors.Is(err, titlefile.ErrRecordTooLarge) {
			continue
		}
		if err != nil {
			return title, fmt.Errorf("scan Pi session names: %w", err)
		}
		if !bytes.Contains(line, []byte(`"session_info"`)) {
			continue
		}
		var entry struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Type == "session_info" {
			title = entry.Name
		}
	}
}
