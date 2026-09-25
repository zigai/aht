package harness

import (
	"context"

	"github.com/zigai/aht/v2/pkg/registry"
)

// TitleReader is an optional adapter capability for native display names.
// Results correspond to identities in order. Missing names are empty; read
// failures return partial results and an error.
type TitleReader interface {
	SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error)
}
