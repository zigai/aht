package broker_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestUnclassifiedRemoteErrorsMatchNoSentinel(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"future_error", "canceled"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()

			remote := &broker.RemoteError{Code: code, Message: "remote diagnostic"}
			err := fmt.Errorf("request failed: %w", remote)

			if !errors.Is(err, remote) {
				t.Fatalf("error = %v, lost original remote error", err)
			}

			for _, sentinel := range []error{
				registry.ErrSessionNotFound,
				registry.ErrObservationConflict,
				context.Canceled,
				context.DeadlineExceeded,
				broker.ErrUnavailable,
				broker.ErrProtocol,
			} {
				if errors.Is(err, sentinel) {
					t.Errorf("code %q incorrectly classified as %v", code, sentinel)
				}
			}
		})
	}
}
