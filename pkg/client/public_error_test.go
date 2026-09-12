package client

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

func TestUnclassifiedRemoteErrorsRemainOperationErrors(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"future_error", "canceled"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			remote := &broker.RemoteError{Code: code, Message: "remote diagnostic"}
			err := publicError(fmt.Errorf("request failed: %w", remote))
			operation, ok := errors.AsType[*OperationError](err)
			if !ok || operation.Code != code || operation.Message != remote.Message {
				t.Fatalf("error = %#v, want operation error with original code and message", err)
			}
			if !errors.Is(err, remote) {
				t.Fatalf("error = %v, lost original remote error", err)
			}
			for _, sentinel := range []error{
				registry.ErrSessionNotFound,
				registry.ErrObservationConflict,
				context.Canceled,
				context.DeadlineExceeded,
				ErrUnavailable,
				ErrProtocol,
			} {
				if errors.Is(err, sentinel) {
					t.Errorf("code %q incorrectly classified as %v", code, sentinel)
				}
			}
		})
	}
}
