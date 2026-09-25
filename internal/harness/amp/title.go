package amp

import (
	"context"
	"fmt"
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (ampHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, fmt.Errorf("lookup Amp session titles: %w", err)
		}
		titles[i] = strings.TrimSpace(identity.Attributes["amp_title"])
	}
	return titles, nil
}
