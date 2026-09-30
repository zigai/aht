package registry_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPresenceValid(t *testing.T) {
	t.Parallel()

	valid := []registry.Presence{
		registry.PresenceLive,
		registry.PresenceGone,
		registry.PresenceUnknown,
	}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("expected presence %q to be valid", p)
		}
	}

	invalid := []registry.Presence{"", "active", "online", "LIVE"}
	for _, p := range invalid {
		if p.Valid() {
			t.Errorf("expected presence %q to be invalid", p)
		}
	}
}

func TestActivityValid(t *testing.T) {
	t.Parallel()

	valid := []registry.Activity{
		registry.ActivityRunning,
		registry.ActivityWaiting,
		registry.ActivityIdle,
		registry.ActivityFailed,
		registry.ActivityInterrupted,
		registry.ActivityUnknown,
	}
	for _, a := range valid {
		if !a.Valid() {
			t.Errorf("expected activity %q to be valid", a)
		}
	}

	invalid := []registry.Activity{"", "busy", "stopped", "RUNNING"}
	for _, a := range invalid {
		if a.Valid() {
			t.Errorf("expected activity %q to be invalid", a)
		}
	}
}

func TestNativeLifecycleValid(t *testing.T) {
	t.Parallel()

	valid := []registry.NativeLifecycle{
		registry.NativeLifecycleStart,
		registry.NativeLifecycleResume,
		registry.NativeLifecycleEnd,
	}
	for _, l := range valid {
		if !l.Valid() {
			t.Errorf("expected lifecycle %q to be valid", l)
		}
	}

	invalid := []registry.NativeLifecycle{"", "stop", "restart"}
	for _, l := range invalid {
		if l.Valid() {
			t.Errorf("expected lifecycle %q to be invalid", l)
		}
	}
}

func TestMultiplexerKindValid(t *testing.T) {
	t.Parallel()

	valid := []registry.MultiplexerKind{
		registry.MultiplexerTmux,
		registry.MultiplexerZellij,
		registry.MultiplexerHerdr,
	}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("expected multiplexer kind %q to be valid", k)
		}
	}

	invalid := []registry.MultiplexerKind{"", "screen", "unknown"}
	for _, k := range invalid {
		if k.Valid() {
			t.Errorf("expected multiplexer kind %q to be invalid", k)
		}
	}
}

func TestFilterUsesSnakeCaseWireNames(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(registry.Filter{Harness: registry.Harness("claude"), Presence: registry.PresenceLive, Activity: registry.ActivityIdle, MultiplexerSession: "work"})
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]string
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	if old["harness"] != "claude" || old["presence"] != "live" || old["activity"] != "idle" || old["multiplexer_session"] != "work" {
		t.Fatalf("invalid filter encoding: %s", encoded)
	}
}

func TestProjectSubtreeAllowsDotPrefixedChild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if !registry.PathWithinOrEqual(filepath.Join(root, "..notes"), root) {
		t.Fatal("legitimate child rejected")
	}
	if registry.PathWithinOrEqual(filepath.Join(root, "..", "sibling"), root) {
		t.Fatal("sibling accepted")
	}
}
