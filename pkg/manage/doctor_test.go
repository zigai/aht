package manage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/pkg/manage"
)

func TestManagerDoctorConciseVsVerbose(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	storePath := filepath.Join(home, "sessions.json")

	// Create a dummy store file with valid schema
	if err := os.WriteFile(storePath, []byte(`{"schema_version":2,"sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := manage.New(manage.Config{
		Binary:             "aht",
		StorePath:          storePath,
		TrackerInterval:    0,
		TrackerGracePeriod: 0,
	})

	t.Run("concise doctor does not include capabilities", func(t *testing.T) {
		t.Parallel()
		res := m.Doctor(context.Background(), manage.DoctorOptions{
			IncludeAll:   false,
			ConfigPath:   filepath.Join(home, "nonexistent.toml"),
			MaxHealthAge: 0,
		})
		if len(res.Capabilities) != 0 {
			t.Fatalf("len(res.Capabilities) = %d, want 0", len(res.Capabilities))
		}
		if len(res.Checks) == 0 {
			t.Fatal("expected doctor checks to be populated")
		}
	})

	t.Run("verbose doctor includes all capabilities", func(t *testing.T) {
		t.Parallel()
		res := m.Doctor(context.Background(), manage.DoctorOptions{
			IncludeAll:   true,
			ConfigPath:   filepath.Join(home, "nonexistent.toml"),
			MaxHealthAge: 0,
		})
		if len(res.Capabilities) == 0 {
			t.Fatal("expected capabilities to be populated in verbose mode")
		}
	})
}

func TestManagerDoctorMissingHealth(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	storePath := filepath.Join(home, "sessions.json")
	if err := os.WriteFile(storePath, []byte(`{"schema_version":2,"sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := manage.New(manage.Config{
		Binary:             "aht",
		StorePath:          storePath,
		TrackerInterval:    0,
		TrackerGracePeriod: 0,
	})

	res := m.Doctor(context.Background(), manage.DoctorOptions{
		IncludeAll:   false,
		ConfigPath:   filepath.Join(home, "nonexistent.toml"),
		MaxHealthAge: 0,
	})

	for _, check := range res.Checks {
		if check.Name == "observer.reconciliation" {
			if check.Status != manage.DoctorStatusWarning {
				t.Fatalf("reconciliation status = %q, want warning", check.Status)
			}
			return
		}
	}
	t.Fatal("observer.reconciliation check was not run")
}

func TestManagerDoctorDegradedHealth(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	storePath := filepath.Join(home, "sessions.json")
	if err := os.WriteFile(storePath, []byte(`{"schema_version":2,"sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	healthPath := storePath + ".observer-health.json"
	degradedData := `{
		"pid": 1234,
		"interval": 300000000,
		"last_enumeration_error": "fatal watcher crash",
		"degraded": true
	}`
	if err := os.WriteFile(healthPath, []byte(degradedData), 0o600); err != nil {
		t.Fatal(err)
	}

	m := manage.New(manage.Config{
		Binary:             "aht",
		StorePath:          storePath,
		TrackerInterval:    0,
		TrackerGracePeriod: 0,
	})

	res := m.Doctor(context.Background(), manage.DoctorOptions{
		IncludeAll:   false,
		ConfigPath:   filepath.Join(home, "nonexistent.toml"),
		MaxHealthAge: 0,
	})

	found := false
	for _, check := range res.Checks {
		if check.Name == "observer.reconciliation" {
			found = true
			if check.Status != manage.DoctorStatusError {
				t.Fatalf("reconciliation status = %q, want error", check.Status)
			}
		}
	}
	if !found {
		t.Fatal("observer.reconciliation check was not run")
	}
	if res.OK {
		t.Fatal("res.OK should be false when degraded health check failed")
	}
}
