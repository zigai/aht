package client

import (
	"context"

	"github.com/zigai/aht/v2/pkg/registry"
)

// TestWatcher provides a deterministic in-memory sessionWatcher for unit tests.
type TestWatcher struct {
	events chan watchEvent
	closed bool
}

func NewTestWatcher() *TestWatcher {
	return &TestWatcher{
		events: make(chan watchEvent, 16),
		closed: false,
	}
}

func (w *TestWatcher) Events() <-chan watchEvent {
	return w.events
}

func (w *TestWatcher) Close() {
	w.closed = true
}

func (w *TestWatcher) SendSessions(sessions ...registry.Session) {
	w.events <- watchEvent{sessions: sessions, err: nil}
}

func (w *TestWatcher) SendError(err error) {
	w.events <- watchEvent{sessions: nil, err: err}
}

func (w *TestWatcher) CloseEvents() {
	close(w.events)
}

func (w *TestWatcher) IsClosed() bool {
	return w.closed
}

// SetWatcherForTest injects a test watcher into Client.
func (c *Client) SetWatcherForTest(w *TestWatcher) {
	c.newWatcher = func(context.Context, registry.Filter) (sessionWatcher, error) {
		return w, nil
	}
}
