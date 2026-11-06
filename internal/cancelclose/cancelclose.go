package cancelclose

import (
	"context"
	"io"
	"sync"
)

// Closer represents a resource whose Close method is safe for concurrent
// and repeated invocations, such as [net.Conn] and [net.Listener].
type Closer interface {
	io.Closer
}

// OnCancel registers context-triggered closure of resource and returns a
// cleanup function that closes the resource and waits for any active
// cancellation callback to complete before returning.
func OnCancel(ctx context.Context, resource Closer) func() {
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
