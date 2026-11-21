package registry

import (
	"context"
	"time"
)

type Store interface {
	Observe(ctx context.Context, observation Observation) (Session, error)
	ObserveBatch(ctx context.Context, observations []Observation) ([]Session, error)
	List(ctx context.Context, filter Filter) ([]Session, error)
	Get(ctx context.Context, id string) (Session, error)
	GC(ctx context.Context, deleteAfter time.Duration) (GCResult, error)
}

type GroupedSummarizer interface {
	SummaryWithOptions(ctx context.Context, filter Filter, opts SummaryOptions) ([]Summary, error)
}
