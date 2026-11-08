package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	watcherBufferSize = 16
	fileWatchDebounce = 10 * time.Millisecond
)

var errHandlerRequired = errors.New("watch handler is required")

//exhaustruct:optional
type watchEvent struct {
	snapshot registry.StateSnapshot
	err      error
}

type sessionWatcher interface {
	Events() <-chan watchEvent
	Close()
}

type brokerSessionWatcher struct {
	subscription *broker.Subscription
	cancel       context.CancelFunc
	events       chan watchEvent
	done         chan struct{}
}

type durableSessionWatcher struct {
	cancel context.CancelFunc
	events chan watchEvent
	done   chan struct{}
}

// sessionStream follows registry snapshots through the configured Mode and
// switches to the durable registry when the broker goes away in ModeAuto.
type sessionStream struct {
	client  *Client
	filter  registry.Filter
	watcher sessionWatcher
	durable bool
}

// Watch calls yield with the initial filtered snapshot and each newer one until
// ctx is canceled. Watch returns nil after cancellation. ModeAuto follows the
// realtime broker and switches to watching the durable registry when the broker
// is unavailable or its stream closes; ModeDurableOnly watches the durable
// registry only. Durable snapshots have no revision.
func (c *Client) Watch(
	ctx context.Context,
	filter registry.Filter,
	yield func(registry.StateSnapshot) error,
) error {
	if c.configErr != nil {
		return c.configErr
	}

	if yield == nil {
		return errHandlerRequired
	}

	stream, err := c.openSessionStream(ctx, filter)
	if err != nil {
		return watchResult(ctx, err)
	}
	defer stream.close()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-stream.events():
			if !ok || ev.err != nil {
				if err := stream.recover(ctx, ok, ev.err); err != nil {
					return watchResult(ctx, err)
				}

				continue
			}

			if err := yield(ev.snapshot); err != nil {
				return fmt.Errorf("handling AHT state snapshot: %w", err)
			}
		}
	}
}

func watchResult(ctx context.Context, err error) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return err
	}
}

func (c *Client) openSessionStream(
	ctx context.Context,
	filter registry.Filter,
) (*sessionStream, error) {
	stream := &sessionStream{client: c, filter: filter, watcher: nil, durable: false}

	switch c.mode {
	case ModeRealtimeOnly:
		watcher, err := openBrokerWatcher(ctx, c.realtime, filter)
		if err != nil {
			return nil, err
		}

		stream.watcher = watcher
	case ModeDurableOnly:
		stream.watcher = startDurableWatcher(ctx, c.storePath, filter)
		stream.durable = true
	case ModeAuto:
		watcher, err := openBrokerWatcher(ctx, c.realtime, filter)
		if err != nil && !brokerOffline(err) {
			return nil, err
		}

		if err != nil {
			stream.watcher = startDurableWatcher(ctx, c.storePath, filter)
			stream.durable = true
		} else {
			stream.watcher = watcher
		}
	default:
		return nil, ErrInvalidMode
	}

	return stream, nil
}

func (s *sessionStream) events() <-chan watchEvent {
	return s.watcher.Events()
}

// recover handles a closed or failed watcher: ModeAuto replaces a lost broker
// stream with a durable watcher; anything else ends the stream with an error.
func (s *sessionStream) recover(ctx context.Context, ok bool, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("watching sessions: %w", ctx.Err())
	}

	if s.client.mode != ModeAuto || s.durable || (ok && !brokerOffline(err)) {
		if !ok {
			return broker.ErrStreamClosed
		}

		return err
	}

	s.watcher.Close()
	s.watcher = startDurableWatcher(ctx, s.client.storePath, s.filter)
	s.durable = true

	return nil
}

func (s *sessionStream) close() {
	s.watcher.Close()
}

func brokerOffline(err error) bool {
	return broker.IsUnavailable(err) || broker.StreamClosed(err)
}

func openBrokerWatcher(
	ctx context.Context,
	brokerClient realtimeClient,
	filter registry.Filter,
) (*brokerSessionWatcher, error) {
	sub, err := brokerClient.Subscribe(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("subscribing to broker: %w", err)
	}

	watcherCtx, cancel := context.WithCancel(ctx)
	w := &brokerSessionWatcher{
		subscription: sub,
		cancel:       cancel,
		events:       make(chan watchEvent, watcherBufferSize),
		done:         make(chan struct{}),
	}

	go w.run(watcherCtx)

	return w, nil
}

func (w *brokerSessionWatcher) Events() <-chan watchEvent {
	return w.events
}

func (w *brokerSessionWatcher) Close() {
	w.cancel()
	w.subscription.Close()
	<-w.done
}

func (w *brokerSessionWatcher) run(ctx context.Context) {
	defer close(w.done)
	defer close(w.events)

	snapshots := w.subscription.Snapshots
	errCh := w.subscription.Errors

	// Subscription errors terminate the stream. Drain snapshots first so a
	// disconnect cannot overtake state already received from the broker.
	for snapshots != nil {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-snapshots:
			if !ok {
				snapshots = nil
				continue
			}

			if !w.forwardEvent(ctx, watchEvent{snapshot: snap, err: nil}) {
				return
			}
		}
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			w.forwardEvent(ctx, watchEvent{err: err})
		}
	}
}

func (w *brokerSessionWatcher) forwardEvent(ctx context.Context, ev watchEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case w.events <- ev:
		return true
	}
}

func startDurableWatcher(
	ctx context.Context,
	storePath string,
	filter registry.Filter,
) *durableSessionWatcher {
	watchCtx, cancel := context.WithCancel(ctx)
	w := &durableSessionWatcher{
		cancel: cancel,
		events: make(chan watchEvent, watcherBufferSize),
		done:   make(chan struct{}),
	}

	go w.run(watchCtx, storePath, filter)

	return w
}

func (w *durableSessionWatcher) Events() <-chan watchEvent {
	return w.events
}

func (w *durableSessionWatcher) Close() {
	w.cancel()
	<-w.done
}

func (w *durableSessionWatcher) deliver(ctx context.Context, event watchEvent) {
	select {
	case <-ctx.Done():
	case w.events <- event:
	}
}

func (w *durableSessionWatcher) run(ctx context.Context, storePath string, filter registry.Filter) {
	defer close(w.done)
	defer close(w.events)

	store := registry.NewJournal(storePath, catalog.Rules{})

	err := store.Watch(ctx, registry.WatchOptions{
		Filter:   filter,
		Debounce: fileWatchDebounce,
	}, func(res registry.WatchResult) error {
		w.deliver(ctx, watchEvent{
			err: res.Err,
			snapshot: registry.StateSnapshot{
				Revision:  0,
				UpdatedAt: res.UpdatedAt,
				Sessions:  res.Sessions,
			},
		})

		return nil
	})
	if err != nil && ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case w.events <- watchEvent{err: err}:
		}
	}
}
