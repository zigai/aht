package fakeprovider

import (
	"context"
	"sync"
)

// Hold is a reply paused until Release.
type Hold struct {
	Request Request
	Stage   Stage

	done chan struct{}
	//exhaustruct:optional
	once sync.Once
}

func newHold(request Request, stage Stage) *Hold {
	return &Hold{
		Request: request.clone(),
		Stage:   stage,
		done:    make(chan struct{}),
	}
}

// Release lets the paused reply continue.
func (h *Hold) Release() {
	h.finish()
}

// Done is closed when the hold ends.
func (h *Hold) Done() <-chan struct{} {
	return h.done
}

func (h *Hold) finish() {
	h.once.Do(func() { close(h.done) })
}

func (s *Server) await(ctx context.Context, gen *session, hold *Hold) bool {
	defer hold.finish()

	select {
	case s.holds <- hold:
	case <-ctx.Done():
		return false
	case <-s.closing:
		return false
	case <-gen.abandoned:
		return false
	}

	select {
	case <-hold.done:
		return true
	case <-ctx.Done():
		return false
	case <-s.closing:
		return false
	case <-gen.abandoned:
		return false
	}
}
