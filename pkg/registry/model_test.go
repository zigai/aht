package registry_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestHarnessIsValid(t *testing.T) {
	t.Parallel()

	all := registry.AllHarnesses()
	if len(all) == 0 {
		t.Fatal("AllHarnesses() returned empty slice")
	}

	for _, h := range all {
		if !h.IsValid() {
			t.Errorf("expected harness %q to be valid", h)
		}
	}

	invalid := []registry.Harness{"", "unknown", "claude-code", "random"}
	for _, h := range invalid {
		if h.IsValid() {
			t.Errorf("expected harness %q to be invalid", h)
		}
	}
}

func TestPresenceIsValid(t *testing.T) {
	t.Parallel()

	valid := []registry.Presence{
		registry.PresenceLive,
		registry.PresenceGone,
		registry.PresenceUnknown,
	}
	for _, p := range valid {
		if !p.IsValid() {
			t.Errorf("expected presence %q to be valid", p)
		}
	}

	invalid := []registry.Presence{"", "active", "online", "LIVE"}
	for _, p := range invalid {
		if p.IsValid() {
			t.Errorf("expected presence %q to be invalid", p)
		}
	}
}

func TestActivityIsValid(t *testing.T) {
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
		if !a.IsValid() {
			t.Errorf("expected activity %q to be valid", a)
		}
	}

	invalid := []registry.Activity{"", "busy", "stopped", "RUNNING"}
	for _, a := range invalid {
		if a.IsValid() {
			t.Errorf("expected activity %q to be invalid", a)
		}
	}
}

func TestObservationSourceIsValid(t *testing.T) {
	t.Parallel()

	valid := []registry.ObservationSource{
		registry.ObservationSourceNative,
		registry.ObservationSourceProcess,
		registry.ObservationSourceTmux,
		registry.ObservationSourceMultiplexer,
		registry.ObservationSourceCatalog,
		registry.ObservationSourceScreen,
	}
	for _, s := range valid {
		if !s.IsValid() {
			t.Errorf("expected source %q to be valid", s)
		}
	}

	invalid := []registry.ObservationSource{"", "file", "unknown"}
	for _, s := range invalid {
		if s.IsValid() {
			t.Errorf("expected source %q to be invalid", s)
		}
	}
}

func TestObservationEvidenceIsValid(t *testing.T) {
	t.Parallel()

	valid := []registry.ObservationEvidence{
		registry.ObservationEvidenceNativeEvent,
		registry.ObservationEvidenceProcessPresence,
		registry.ObservationEvidenceTmuxLocation,
		registry.ObservationEvidenceMultiplexerLocation,
		registry.ObservationEvidenceCatalogMetadata,
		registry.ObservationEvidenceScreenState,
	}
	for _, e := range valid {
		if !e.IsValid() {
			t.Errorf("expected evidence %q to be valid", e)
		}
	}

	invalid := []registry.ObservationEvidence{"", "event", "unknown"}
	for _, e := range invalid {
		if e.IsValid() {
			t.Errorf("expected evidence %q to be invalid", e)
		}
	}
}

func TestNativeLifecycleIsValid(t *testing.T) {
	t.Parallel()

	valid := []registry.NativeLifecycle{
		registry.NativeLifecycleStart,
		registry.NativeLifecycleResume,
		registry.NativeLifecycleEnd,
	}
	for _, l := range valid {
		if !l.IsValid() {
			t.Errorf("expected lifecycle %q to be valid", l)
		}
	}

	invalid := []registry.NativeLifecycle{"", "stop", "restart"}
	for _, l := range invalid {
		if l.IsValid() {
			t.Errorf("expected lifecycle %q to be invalid", l)
		}
	}
}

func TestMultiplexerKindIsValid(t *testing.T) {
	t.Parallel()

	valid := []registry.MultiplexerKind{
		registry.MultiplexerTmux,
		registry.MultiplexerZellij,
		registry.MultiplexerHerdr,
	}
	for _, k := range valid {
		if !k.IsValid() {
			t.Errorf("expected multiplexer kind %q to be valid", k)
		}
	}

	invalid := []registry.MultiplexerKind{"", "screen", "unknown"}
	for _, k := range invalid {
		if k.IsValid() {
			t.Errorf("expected multiplexer kind %q to be invalid", k)
		}
	}
}

func TestAllHarnessesCloned(t *testing.T) {
	t.Parallel()

	first := registry.AllHarnesses()
	second := registry.AllHarnesses()

	if !slices.Equal(first, second) {
		t.Fatalf("expected identical slices, got %v and %v", first, second)
	}

	first[0] = "mutated"
	if slices.Equal(first, registry.AllHarnesses()) {
		t.Fatal("AllHarnesses did not return an isolated clone")
	}
}

func TestFilterPreservesBrokerWireNames(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(registry.Filter{Harness: registry.HarnessClaude, Presence: registry.PresenceLive, Activity: registry.ActivityIdle, TmuxSession: "work", MultiplexerSession: "work"})
	if err != nil {
		t.Fatal(err)
	}
	var old map[string]string
	if err := json.Unmarshal(encoded, &old); err != nil {
		t.Fatal(err)
	}
	if old["Harness"] != "claude" || old["Presence"] != "live" || old["Activity"] != "idle" || old["TmuxSession"] != "work" || old["MultiplexerSession"] != "work" {
		t.Fatalf("old broker could not decode filter: %s", encoded)
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
