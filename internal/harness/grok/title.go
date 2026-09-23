package grok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness/titlefile"
	"github.com/zigai/aht/pkg/registry"
)

const maxGrokSummaryBytes = 64 << 10

var (
	errGrokSummaryTooLarge        = errors.New("grok session summary exceeds 64 KiB")
	errGrokMultipleSummariesMatch = errors.New("multiple Grok summaries match session")
)

func (grokHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if !validGrokSessionID(identity.SessionID) {
			continue
		}
		paths, err := grokSummaryPaths(identity)
		if err != nil {
			failures = append(failures, fmt.Errorf("find Grok session title: %w", err))
			continue
		}
		title, err := grokMatchingSessionTitle(ctx, identity.SessionID, paths)
		if err != nil {
			failures = append(failures, err)
		}
		titles[i] = title
	}
	return titles, errors.Join(failures...)
}

func validGrokSessionID(sessionID string) bool {
	return sessionID != "" && filepath.Base(sessionID) == sessionID && sessionID != "." && sessionID != ".."
}

func grokMatchingSessionTitle(ctx context.Context, sessionID string, paths []string) (string, error) {
	var failures []error
	title := ""
	matched := false
	for _, path := range paths {
		candidate, found, err := readGrokSessionTitle(ctx, sessionID, path)
		if err != nil {
			failures = append(failures, fmt.Errorf("read Grok session title: %w", err))
			continue
		}
		if !found {
			continue
		}
		if matched && title != candidate {
			title = ""
			failures = append(failures, fmt.Errorf("%w %q", errGrokMultipleSummariesMatch, sessionID))
			break
		}
		matched = true
		title = candidate
	}
	return title, errors.Join(failures...)
}

func grokSummaryPaths(identity registry.ObservationIdentity) ([]string, error) {
	if identity.SessionPath != "" {
		if filepath.Base(identity.SessionPath) == "summary.json" && filepath.Base(filepath.Dir(identity.SessionPath)) == identity.SessionID {
			return []string{identity.SessionPath}, nil
		}
		if filepath.Base(identity.SessionPath) == identity.SessionID {
			return []string{filepath.Join(identity.SessionPath, "summary.json")}, nil
		}
		return nil, nil
	}

	root := filepath.Join(grokHome(), "sessions")
	groups, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Grok session directory: %w", err)
	}
	paths := make([]string, 0, 1)
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(root, group.Name(), identity.SessionID, "summary.json"))
	}
	return paths, nil
}

func readGrokSessionTitle(ctx context.Context, sessionID, path string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, fmt.Errorf("read Grok session summary: %w", err)
	}
	file, err := titlefile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open Grok session summary: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxGrokSummaryBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("read Grok session summary: %w", err)
	}
	if len(data) > maxGrokSummaryBytes {
		return "", false, errGrokSummaryTooLarge
	}
	var summary struct {
		Info struct {
			SessionID string `json:"session_id"`
		} `json:"info"`
		GeneratedTitle string `json:"generated_title"`
		SessionSummary string `json:"session_summary"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return "", false, fmt.Errorf("decode Grok session summary: %w", err)
	}
	if summary.Info.SessionID != sessionID {
		return "", false, nil
	}
	title := strings.TrimSpace(summary.GeneratedTitle)
	if title == "" {
		title = strings.TrimSpace(summary.SessionSummary)
	}
	return title, true, nil
}
