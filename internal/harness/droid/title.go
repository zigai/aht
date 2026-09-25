package droid

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/titlefile"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (droidHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if identity.SessionID == "" || filepath.Base(identity.SessionID) != identity.SessionID || identity.SessionID == "." || identity.SessionID == ".." {
			continue
		}
		paths, err := droidSessionPaths(identity)
		if err != nil {
			failures = append(failures, fmt.Errorf("find Droid session title: %w", err))
			continue
		}
		for _, path := range paths {
			title, err := readDroidSessionTitle(ctx, identity.SessionID, path)
			if err != nil {
				failures = append(failures, fmt.Errorf("read Droid session title: %w", err))
				continue
			}
			if title != "" {
				titles[i] = title
				break
			}
		}
	}
	return titles, errors.Join(failures...)
}

func droidSessionPaths(identity registry.ObservationIdentity) ([]string, error) {
	if identity.SessionPath != "" {
		return []string{identity.SessionPath}, nil
	}
	root := droidSessionsDir()
	paths := []string{filepath.Join(root, identity.SessionID+".jsonl")}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return paths, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Droid sessions directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "-") {
			paths = append(paths, filepath.Join(root, entry.Name(), identity.SessionID+".jsonl"))
		}
	}
	return paths, nil
}

func readDroidSessionTitle(ctx context.Context, sessionID, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read Droid session: %w", err)
	}
	file, err := titlefile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open Droid session: %w", err)
	}
	defer func() { _ = file.Close() }()
	line, err := titlefile.ReadLine(ctx, bufio.NewReader(file))
	if errors.Is(err, io.EOF) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Droid session metadata: %w", err)
	}
	var metadata struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(line, &metadata); err != nil {
		return "", fmt.Errorf("decode Droid session metadata: %w", err)
	}
	if metadata.Type != "session_start" || metadata.ID != sessionID {
		return "", nil
	}
	return metadata.Title, nil
}

func droidSessionsDir() string {
	if value := strings.TrimSpace(os.Getenv("DROID_SESSIONS_DIR")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "."
	}
	return filepath.Join(home, ".factory", "sessions")
}
