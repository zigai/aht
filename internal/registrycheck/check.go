package registrycheck

import (
	"fmt"

	"github.com/zigai/aht/v2/internal/registrycheck/core"
	"github.com/zigai/aht/v2/pkg/registry"
)

type Coverage = core.Coverage

// Sessions checks properties preserved by the reducer and snapshot validation.
func Sessions(sessions []registry.Session) error {
	projected := make([]core.Session, len(sessions))
	for index, session := range sessions {
		var activity string
		if value := session.Activity(); value != nil {
			activity = string(*value)
		}
		projected[index] = core.Session{
			ID: session.ID, Harness: string(session.Harness), CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
			HasLiveness: session.Liveness != nil, Presence: string(session.Presence()), Activity: activity,
			ValidState:  session.Presence().Valid() && (session.Activity() == nil || session.Activity().Valid()),
			NativeEnded: session.Observations.Native != nil && session.Observations.Native.Lifecycle != nil && *session.Observations.Native.Lifecycle == registry.NativeLifecycleEnd,
		}
	}
	if err := core.Sessions(projected); err != nil {
		return fmt.Errorf("registry sessions: %w", err)
	}
	return nil
}

func NewCoverage() *Coverage { return core.NewCoverage() }
