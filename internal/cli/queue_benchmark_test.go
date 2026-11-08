package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/reporting"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func BenchmarkPrepareReport(b *testing.B) {
	options := reporting.Options{
		Harness:   "codex",
		SessionID: "benchmark",
		Event:     "turn_start",
		Activity:  "running",
	}
	for b.Loop() {
		if _, err := reporting.Prepare(
			nil,
			options,
			reporting.Runtime{DefaultObservedAt: time.Now().UTC()},
		); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObserveBatch(b *testing.B) {
	store := registry.NewJournal(filepath.Join(b.TempDir(), "sessions.json"), catalog.Rules{})
	observation := registry.Observation{
		Harness:  registry.Harness("codex"),
		At:       time.Now().UTC(),
		Subject:  registry.ObservationIdentity{SessionID: "benchmark"},
		Evidence: &registry.Report{Event: "turn_start"},
	}

	b.ResetTimer()

	for b.Loop() {
		if _, err := store.Observe(context.Background(), observation); err != nil {
			b.Fatal(err)
		}
	}
}
