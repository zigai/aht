package aht

import (
	"context"
	"errors"
	"fmt"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// SessionTitles returns native display titles in session order. The caller can
// pair titles with sessions by index. A native SessionID is required. An empty
// title means no title is recorded or the harness has no title reader.
// [Capabilities] reports which harnesses support lookup. This function reads
// harness metadata when called and does not change registry List or Watch results.
// Titles are display text, never session or resume identities. Claude's persistent
// OS cache assumes append-only growth; replacement, truncation and same-size
// rewrites reset it. Native file-watch hooks refresh that cache. Pi still scans
// transcripts; latency-sensitive callers should cache its results. Successful
// titles remain alongside read or cache errors.
func SessionTitles(ctx context.Context, sessions []Session) ([]string, error) {
	titles := make([]string, len(sessions))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("read session titles: %w", err)
	}
	byHarness := make(map[registry.Harness][]int)
	for i, session := range sessions {
		if session.SessionID != "" {
			byHarness[session.Harness] = append(byHarness[session.Harness], i)
		}
	}
	var failures []error
	for id, indices := range byHarness {
		adapter, ok := catalog.Find(id)
		if !ok {
			continue
		}
		reader, ok := adapter.(harness.TitleReader)
		if !ok {
			continue
		}
		batch := make([]registry.ObservationIdentity, len(indices))
		for i, index := range indices {
			var attributes map[string]string
			if native := sessions[index].Observations.Native; native != nil {
				attributes = native.Attributes
			}
			batch[i] = registry.ObservationIdentity{
				SessionID:   sessions[index].SessionID,
				SessionPath: sessions[index].SessionPath,
				CWD:         sessions[index].CWD,
				Attributes:  attributes,
			}
		}
		found, err := reader.SessionTitles(ctx, batch)
		for i, index := range indices {
			titles[index] = found[i]
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("lookup %s session titles: %w", id, err))
		}
	}
	return titles, errors.Join(failures...)
}
