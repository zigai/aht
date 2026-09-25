package opencode

import (
	"context"
	"fmt"
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (opencodeHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, fmt.Errorf("lookup OpenCode session titles: %w", err)
		}
		titles[i] = strings.TrimSpace(identity.Attributes["opencode_title"])
	}
	return titles, nil
}
