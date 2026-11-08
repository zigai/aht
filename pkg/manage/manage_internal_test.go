package manage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrackerBlankDurationsUseDefaults(t *testing.T) {
	manager := New(Config{
		Binary: "/bin/sh", StorePath: "/state.json", ConfigPath: "-",
		ConfigStdin: strings.NewReader("[tracker]\ninterval = ''\ngrace_period = ''\n"),
	})

	got, err := manager.serviceOptions(TrackerOptions{DryRun: true})
	if err != nil || got.Interval != 300*time.Millisecond || got.GracePeriod != 0 {
		t.Fatalf("blank tracker durations = %+v, %v", got, err)
	}
}

func TestTrackerOptionsUseConfigAndExplicitOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	err := os.WriteFile(
		path,
		[]byte("[tracker]\ninterval = \"2s\"\ngrace_period = \"5s\"\n"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}

	configured := New(Config{Binary: "/aht", StorePath: "/state.json", ConfigPath: path})

	got, err := configured.serviceOptions(TrackerOptions{DryRun: false})
	if err != nil || got.Interval != 2*time.Second || got.GracePeriod != 5*time.Second {
		t.Fatalf("configured tracker = %#v, %v", got, err)
	}

	zero := time.Duration(0)
	overridden := New(Config{
		Binary:             "/aht",
		StorePath:          "/state.json",
		ConfigPath:         path,
		TrackerInterval:    time.Second,
		TrackerGracePeriod: &zero,
	})

	got, err = overridden.serviceOptions(TrackerOptions{DryRun: false})
	if err != nil || got.Interval != time.Second || got.GracePeriod != 0 {
		t.Fatalf("overridden tracker = %#v, %v", got, err)
	}
}
