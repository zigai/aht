package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func BenchmarkPrepareReport(b *testing.B) {
	options := reportOptions{harness: "codex", sessionID: "benchmark", event: "turn_start", activity: "running"}
	for b.Loop() {
		if _, err := prepareReport(nil, options, reportRuntimeContext{defaultObservedAt: time.Now().UTC()}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObserveBatch(b *testing.B) {
	store := registry.NewJournal(filepath.Join(b.TempDir(), "sessions.json"), catalog.Rules{})
	observation := registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "benchmark"}, Evidence: &registry.Report{Event: "turn_start"}}
	b.ResetTimer()
	for b.Loop() {
		if _, err := store.Observe(context.Background(), observation); err != nil {
			b.Fatal(err)
		}
	}
}
