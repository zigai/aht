package observer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDecodeCatalogArrayAndEnvelope(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`[{"harness":"claude","session_id":"one","current":true}]`,
		`{"sessions":[{"harness":"goose","session_id":"two","current":false}]}`,
	} {
		entries, err := decodeCatalog([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].SessionID == "" {
			t.Fatalf("unexpected entries: %#v", entries)
		}
	}
}

func TestDefaultCatalogListRejectsUnknownHarness(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{"harness":"unknown","session_id":"bad"}]`)
	t.Setenv(catalogFileEnv, "")
	if _, err := DefaultCatalogList(context.Background()); err == nil {
		t.Fatal("expected unknown harness error")
	}
}

func TestDefaultCatalogListCanonicalizesHarnessAlias(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{"harness":"claude-code","session_id":"alias"}]`)
	t.Setenv(catalogFileEnv, "")

	entries, err := DefaultCatalogList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Harness != "claude" {
		t.Fatalf("canonical entries = %#v", entries)
	}
}

func TestObserverCycleAcceptsCanonicalizedCatalogAlias(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{"harness":"claude-code","session_id":"alias","current":true}]`)
	t.Setenv(catalogFileEnv, "")
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	watcher := New(Options{
		Store:       store,
		StorePath:   path,
		HealthPath:  path + ".health",
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: DefaultCatalogList,
		Now:         func() time.Time { return time.Now().UTC() },
	})
	result, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Degraded {
		t.Fatalf("catalog alias degraded observer cycle: %#v", result)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Harness != registry.Harness("claude") {
		t.Fatalf("catalog sessions = %#v", sessions)
	}
}

func TestDefaultCatalogListEmptyWithoutConfiguration(t *testing.T) {
	t.Setenv(catalogJSONEnv, "")
	t.Setenv(catalogFileEnv, "")
	entries, err := DefaultCatalogList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Fatalf("expected nil catalog, got %#v", entries)
	}
}

func TestDecodeCatalogRejectsNull(t *testing.T) {
	t.Parallel()

	if _, err := decodeCatalog([]byte("null")); !errors.Is(err, errCatalogSessionsType) {
		t.Fatalf("decodeCatalog() error = %v, want %v", err, errCatalogSessionsType)
	}
}

func TestDefaultCatalogListRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxCatalogBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(catalogJSONEnv, "")
	t.Setenv(catalogFileEnv, path)

	_, err := DefaultCatalogList(context.Background())
	if !errors.Is(err, errCatalogTooLarge) {
		t.Fatalf("DefaultCatalogList() error = %v, want %v", err, errCatalogTooLarge)
	}
}
