package kimi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (kimiCodeHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Kimi Code session titles: %w", err)
	}
	var failures []error
	for index, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if identity.SessionID == "" {
			continue
		}
		sessionDir, err := kimiTitleSessionDir(identity)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if sessionDir == "" {
			continue
		}
		state, err := readKimiSessionState(sessionDir, identity.SessionID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("lookup Kimi Code session title: %w", err))
			continue
		}
		titles[index] = strings.TrimSpace(state.Title)
	}
	return titles, errors.Join(failures...)
}

func kimiTitleSessionDir(identity registry.ObservationIdentity) (string, error) {
	sessionDir := identity.SessionPath
	if sessionDir == "" {
		return kimiCodeSessionPath(identity.SessionID)
	}
	switch filepath.Base(sessionDir) {
	case "wire.jsonl":
		sessionDir = kimiTranscriptSessionDir(sessionDir)
	case "state.json":
		sessionDir = filepath.Dir(sessionDir)
	}
	if sessionDir == "" || filepath.Base(sessionDir) != identity.SessionID {
		return "", nil
	}
	return sessionDir, nil
}
