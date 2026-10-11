package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

// Sessions returns the AHT sessions of this harness in Work.
func (h *Harness) Sessions(ctx context.Context) ([]registry.Session, error) {
	if h.aht == "" {
		return nil, errNoAHT
	}
	var stdout, stderr bytes.Buffer
	cmd := h.AHTCommand(ctx, "--json", "list", "--agent", string(h.ID))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("list sessions: %w: %s", err, stderr.String())
	}
	var sessions []registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		return nil, fmt.Errorf("decode sessions: %w", err)
	}
	matching := sessions[:0]
	for _, session := range sessions {
		if filepath.Clean(session.CWD) != h.Work {
			continue
		}
		if native := session.Observations.Native; native != nil {
			native.RawPayload = nil
		}
		matching = append(matching, session)
	}
	return matching, nil
}

// WaitForSession waits until match accepts a session.
func (h *Harness) WaitForSession(ctx context.Context, match func(registry.Session) bool) (registry.Session, error) {
	if match == nil {
		return registry.Session{}, errNilMatch
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var latest []registry.Session
	for {
		sessions, err := h.Sessions(ctx)
		if err != nil && ctx.Err() == nil {
			return registry.Session{}, fmt.Errorf("read sessions: %w", err)
		}
		if err == nil {
			latest = sessions
			for _, session := range sessions {
				if match(session) {
					return session, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			encoded, encodeErr := json.MarshalIndent(latest, "", "  ")
			if encodeErr != nil {
				return registry.Session{}, errors.Join(ctx.Err(), fmt.Errorf("encode last sessions: %w", encodeErr))
			}
			return registry.Session{}, fmt.Errorf("wait for session: %w; last sessions:\n%s", ctx.Err(), encoded)
		case <-ticker.C:
		}
	}
}
