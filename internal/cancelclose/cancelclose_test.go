package cancelclose_test

import (
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/cancelclose"
)

type nopCloser struct{}

func (nopCloser) Close() error {
	return nil
}

func TestCleanupIsSafeToCallTwice(t *testing.T) {
	cleanup := cancelclose.OnCancel(t.Context(), nopCloser{})

	cleanup()

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		cleanup()
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("second cleanup call did not return within 1s")
	}
}
