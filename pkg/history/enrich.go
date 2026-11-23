package history

import (
	"context"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

// enrich adds resume commands and native titles stored outside transcripts.
// Titles are display metadata, so a failed lookup keeps the indexed title.
func enrich(ctx context.Context, matches []Match) {
	lookups := map[registry.Harness][]int{}

	for i := range matches {
		c := matches[i].Conversation

		path := c.Path
		if adapters.TranscriptFor(c.Harness).Format(path) == transcript.FormatDatabase {
			path = ""
		}

		matches[i].ResumeCommand = adapters.ResumeCommandFor(c.Harness, c.SessionID, path)
		if adapters.TranscriptFor(c.Harness).LocalTitles {
			lookups[c.Harness] = append(lookups[c.Harness], i)
		}
	}

	for id, indices := range lookups {
		found, ok := adapters.Find(id)
		if !ok {
			continue
		}

		reader, ok := found.(harness.TitleReader)
		if !ok {
			continue
		}

		identities := make([]registry.ObservationIdentity, len(indices))
		for j, i := range indices {
			c := matches[i].Conversation
			identities[j] = registry.ObservationIdentity{
				SessionID:   c.SessionID,
				SessionPath: c.Path,
				CWD:         c.CWD,
				Attributes:  nil,
			}
		}

		titles, _ := reader.SessionTitles(ctx, identities)
		for j, i := range indices {
			if j < len(titles) && titles[j] != "" {
				matches[i].Conversation.Title = titles[j]
			}
		}
	}
}
