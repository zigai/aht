package broker

import (
	"context"
	"fmt"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	_ registry.Store             = (*Store)(nil)
	_ registry.GroupedSummarizer = (*Store)(nil)
)

// Store routes operations through the realtime broker and falls back to the
// durable snapshot when the broker is offline. The fallback keeps one-shot CLI
// use functional; a running broker remains the authoritative hot path.
type Store struct {
	client   *Client
	fallback *registry.Journal
}

// NewStore returns a broker-backed registry store for storePath.
func NewStore(storePath string) *Store {
	return NewStoreForSocket(storePath, SocketPath(storePath))
}

func NewStoreForSocket(storePath string, socketPath string) *Store {
	return &Store{
		client:   NewClientForSocket(socketPath),
		fallback: registry.NewJournal(storePath, catalog.Rules{}),
	}
}

// Client returns the realtime client used by the store.
func (s *Store) Client() *Client { return s.client }

func (s *Store) Observe(ctx context.Context, observation registry.Observation) (registry.Session, error) {
	session, err := s.client.Observe(ctx, observation)
	if !IsUnavailable(err) {
		return session, err
	}

	session, err = s.fallback.Observe(ctx, observation)
	if err != nil {
		return registry.Session{}, fmt.Errorf("recording fallback observation: %w", err)
	}

	return session, nil
}

func (s *Store) ObserveBatch(
	ctx context.Context,
	observations []registry.Observation,
) ([]registry.Session, error) {
	sessions, err := s.client.ObserveBatch(ctx, observations)
	if !IsUnavailable(err) {
		return sessions, err
	}

	sessions, err = s.fallback.Append(ctx, observations)
	if err != nil {
		return nil, fmt.Errorf("recording fallback observations: %w", err)
	}

	return sessions, nil
}

func (s *Store) List(ctx context.Context, filter registry.Filter) ([]registry.Session, error) {
	sessions, err := s.client.List(ctx, filter)
	if !IsUnavailable(err) {
		return sessions, err
	}

	sessions, err = s.fallback.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("listing fallback registry: %w", err)
	}

	return sessions, nil
}

func (s *Store) Get(ctx context.Context, id string) (registry.Session, error) {
	session, err := s.client.Get(ctx, id)
	if !IsUnavailable(err) {
		return session, err
	}

	session, err = s.fallback.Get(ctx, id)
	if err != nil {
		return registry.Session{}, fmt.Errorf("getting fallback session: %w", err)
	}

	return session, nil
}

// SummaryWithOptions returns filtered summaries with options from broker or fallback.
func (s *Store) SummaryWithOptions(
	ctx context.Context,
	filter registry.Filter,
	opts registry.SummaryOptions,
) ([]registry.Summary, error) {
	summaries, err := s.client.SummaryWithOptions(ctx, filter, opts)
	if !IsUnavailable(err) {
		return summaries, err
	}
	summaries, err = s.fallback.SummaryWithOptions(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("summarizing fallback registry: %w", err)
	}

	return summaries, nil
}

func (s *Store) GC(ctx context.Context, deleteAfter time.Duration) (registry.GCResult, error) {
	result, err := s.client.GC(ctx, deleteAfter)
	if !IsUnavailable(err) {
		return result, err
	}

	result, err = s.fallback.GC(ctx, deleteAfter)
	if err != nil {
		return registry.GCResult{}, fmt.Errorf("cleaning fallback registry: %w", err)
	}

	return result, nil
}

func (s *Store) Reset(ctx context.Context) (registry.ResetResult, error) {
	result, err := s.client.Reset(ctx)
	if !IsUnavailable(err) {
		return result, err
	}

	result, err = s.fallback.Reset(ctx)
	if err != nil {
		return registry.ResetResult{}, fmt.Errorf("resetting fallback registry: %w", err)
	}

	return result, nil
}
