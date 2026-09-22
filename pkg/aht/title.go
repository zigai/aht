package aht

import (
	"context"
	"errors"
	"fmt"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

// LookupTitles returns native display titles in session order. The caller can
// pair titles with sessions by index. A native SessionID is required. An empty
// title means no title is recorded or the harness has no title reader.
// [Capabilities] reports which harnesses support lookup. This function reads
// harness metadata when called and does not change registry List or Watch results.
// Titles are display text, never session or resume identities. Pi's current name
// requires a transcript scan; callers rechecking the same file in a
// latency-sensitive path should cache the result. Successful titles remain in
// the result when another source returns a read error.
func LookupTitles(ctx context.Context, sessions []Session) ([]string, error) {
	titles := make([]string, len(sessions))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup session titles: %w", err)
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
			batch[i] = registry.ObservationIdentity{
				SessionID:   sessions[index].SessionID,
				SessionPath: sessions[index].SessionPath,
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
