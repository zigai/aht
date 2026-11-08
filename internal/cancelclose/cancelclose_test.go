package cancelclose_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/zigai/aht/v2/internal/cancelclose"
)

func TestCleanupIsSafeToCallTwice(t *testing.T) {
	for _, trigger := range []string{"cleanup", "cancel"} {
		t.Run(trigger, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				reader, writer := io.Pipe()
				defer func() { _ = reader.Close() }()

				cleanup := cancelclose.OnCancel(ctx, writer)
				defer cleanup()

				read := make(chan error, 1)

				go func() { var data [1]byte; _, err := reader.Read(data[:]); read <- err }()

				if trigger == "cancel" {
					cancel()
				} else {
					cleanup()
				}

				synctest.Wait()

				select {
				case err := <-read:
					if !errors.Is(err, io.EOF) {
						t.Fatalf("read after %s = %v, want EOF", trigger, err)
					}
				default:
					t.Fatalf("%s did not close the resource", trigger)
				}

				cleanup()
				cleanup()
			})
		})
	}
}
