package cancelclose

import (
	"context"
	"io"
	"sync"
)

// OnCancel registers context-triggered closure of resource and returns a
// cleanup function that closes the resource and waits for any active
// cancellation callback to complete before returning. resource.Close must be
// safe for concurrent and repeated calls, as it is for [net.Conn] and
// [net.Listener].
func OnCancel(ctx context.Context, resource io.Closer) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)

		_ = resource.Close()
	})

	var once sync.Once

	return func() {
		once.Do(func() {
			_ = resource.Close()

			if !stop() {
				<-done
			}
		})
	}
}
