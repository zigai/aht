package manage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/manage"
	"github.com/zigai/aht/pkg/registry"
)

func TestExplainHookAuthority(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{
		PID:           1001,
		PPID:          1,
		StartIdentity: "boot:1001",
		Executable:    "pi",
		Foreground:    true,
		TTY:           "/dev/pts/1",
	}
	running := registry.ActivityRunning

	session := registry.Session{
		ID:       "s-hook",
		Harness:  registry.Harness("pi"),
		Process:  &process,
		Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%1", PaneTTY: "/dev/pts/1"},
		Observations: registry.Observations{
			Native: &registry.NativeObservation{
				Event:      "agent_start",
				Activity:   &running,
				Process:    process,
				ObservedAt: now.Add(-2 * time.Second),
				Reporter:   registry.Reporter{Integration: "pi-extension"},
			},
		},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
	}

	exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp.SelectedAuthority != "hook" {
		t.Fatalf("SelectedAuthority = %q, want hook", exp.SelectedAuthority)
	}
	if exp.FallbackReason != "" {
		t.Fatalf("FallbackReason = %q, want empty", exp.FallbackReason)
	}
	if !exp.Hook.Active || !exp.Hook.Fresh || !exp.Hook.ProcessMatches {
		t.Fatalf("unexpected hook explanation: %+v", exp.Hook)
	}
	if exp.FinalActivity != "running" {
		t.Fatalf("FinalActivity = %q, want running", exp.FinalActivity)
	}
}

func TestExplainFallbackToScreenAuthority(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{
		PID:           1001,
		PPID:          1,
		StartIdentity: "boot:1001",
		Executable:    "pi",
		Foreground:    true,
		TTY:           "/dev/pts/1",
	}
	running := registry.ActivityRunning

	session := registry.Session{
		ID:       "s-fallback",
		Harness:  registry.Harness("pi"),
		Process:  &process,
		Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%1", PaneTTY: "/dev/pts/1"},
		Observations: registry.Observations{
			Native: &registry.NativeObservation{
				Event:      "agent_start",
				Activity:   &running,
				Process:    process,
				ObservedAt: now.Add(-35 * time.Second), // Stale (exceeds 30s lease)
				Reporter:   registry.Reporter{Integration: "pi-extension"},
			},
		},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
	}

	exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp.SelectedAuthority != "screen" {
		t.Fatalf("SelectedAuthority = %q, want screen", exp.SelectedAuthority)
	}
	if exp.FallbackReason != "integration_report_stale" {
		t.Fatalf("FallbackReason = %q, want integration_report_stale", exp.FallbackReason)
	}
	if exp.Hook.Active {
		t.Fatalf("Hook.Active = true, want false")
	}
}

func TestExplainScreenAuthorityDirectly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	running := registry.ActivityRunning
	codexProc := registry.ProcessIdentity{
		PID:           2002,
		PPID:          1,
		StartIdentity: "boot:2002",
		Executable:    "codex",
	}
	session := registry.Session{
		ID:       "s-screen",
		Harness:  registry.Harness("codex"),
		Process:  &codexProc,
		Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%2"},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
	}

	exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp.SelectedAuthority != "screen" {
		t.Fatalf("SelectedAuthority = %q, want screen", exp.SelectedAuthority)
	}
	if exp.FallbackReason != "" {
		t.Fatalf("FallbackReason = %q, want empty", exp.FallbackReason)
	}
}

func TestExplainExpiredAndFutureEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{
		PID:           1001,
		PPID:          1,
		StartIdentity: "boot:1001",
		Executable:    "pi",
	}
	running := registry.ActivityRunning

	t.Run("future evidence rejected as inactive", func(t *testing.T) {
		t.Parallel()
		session := registry.Session{
			ID:      "s-future",
			Harness: registry.Harness("pi"),
			Process: &process,
			Observations: registry.Observations{
				Native: &registry.NativeObservation{
					Event:      "agent_start",
					Activity:   &running,
					Process:    process,
					ObservedAt: now.Add(10 * time.Second), // In future!
					Reporter:   registry.Reporter{Integration: "pi-extension"},
				},
			},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
		}

		exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exp.Hook.Active {
			t.Fatal("future evidence should not be active")
		}
		if exp.Hook.Fresh {
			t.Fatal("future evidence should not be fresh")
		}
		if exp.Hook.FreshnessReason != "integration_observation_from_future" {
			t.Fatalf("FreshnessReason = %q, want integration_observation_from_future", exp.Hook.FreshnessReason)
		}
	})

	t.Run("expired evidence rejected as stale", func(t *testing.T) {
		t.Parallel()
		session := registry.Session{
			ID:      "s-expired",
			Harness: registry.Harness("pi"),
			Process: &process,
			Observations: registry.Observations{
				Native: &registry.NativeObservation{
					Event:      "agent_start",
					Activity:   &running,
					Process:    process,
					ObservedAt: now.Add(-45 * time.Second), // Exceeds 30s lease
					Reporter:   registry.Reporter{Integration: "pi-extension"},
				},
			},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
		}

		exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exp.Hook.Active {
			t.Fatal("expired evidence should not be active")
		}
		if exp.Hook.FreshnessReason != "integration_report_stale" {
			t.Fatalf("FreshnessReason = %q, want integration_report_stale", exp.Hook.FreshnessReason)
		}
	})
}

func TestExplainIntegrationMismatchAndPIDReuse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	running := registry.ActivityRunning

	t.Run("integration identity mismatch", func(t *testing.T) {
		t.Parallel()
		process := registry.ProcessIdentity{
			PID:           1001,
			PPID:          1,
			StartIdentity: "boot:1001",
			Executable:    "pi",
		}
		session := registry.Session{
			ID:      "s-mismatch",
			Harness: registry.Harness("pi"),
			Process: &process,
			Observations: registry.Observations{
				Native: &registry.NativeObservation{
					Event:      "agent_start",
					Activity:   &running,
					Process:    process,
					ObservedAt: now.Add(-2 * time.Second),
					Reporter:   registry.Reporter{Integration: "rogue-plugin"},
				},
			},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
		}

		exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exp.Hook.Active {
			t.Fatal("mismatched integration should not be active")
		}
		if exp.Hook.FreshnessReason != "integration_identity_mismatch" {
			t.Fatalf("FreshnessReason = %q, want integration_identity_mismatch", exp.Hook.FreshnessReason)
		}
	})

	t.Run("PID reuse with different start identity", func(t *testing.T) {
		t.Parallel()
		currentProc := registry.ProcessIdentity{
			PID:           1001,
			PPID:          1,
			StartIdentity: "boot:1001:new",
			Executable:    "pi",
		}
		staleHookProc := registry.ProcessIdentity{
			PID:           1001,
			PPID:          1,
			StartIdentity: "boot:1001:old",
			Executable:    "pi",
		}
		session := registry.Session{
			ID:      "s-pid-reuse",
			Harness: registry.Harness("pi"),
			Process: &currentProc,
			Observations: registry.Observations{
				Native: &registry.NativeObservation{
					Event:      "agent_start",
					Activity:   &running,
					Process:    staleHookProc,
					ObservedAt: now.Add(-2 * time.Second),
					Reporter:   registry.Reporter{Integration: "pi-extension"},
				},
			},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), nil),
		}

		exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exp.Hook.Active {
			t.Fatal("replaced process hook should not be active")
		}
		if exp.Hook.ProcessMatches {
			t.Fatal("ProcessMatches should be false")
		}
		if exp.Hook.FreshnessReason != "agent_process_replaced" {
			t.Fatalf("FreshnessReason = %q, want agent_process_replaced", exp.Hook.FreshnessReason)
		}
	})
}

func TestExplainEndedSessions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	running := registry.ActivityRunning
	process := registry.ProcessIdentity{PID: 1001, StartIdentity: "boot:1001", Executable: "pi"}
	endLifecycle := registry.NativeLifecycleEnd

	cases := []struct {
		name      string
		presence  registry.Presence
		lifecycle *registry.NativeLifecycle
	}{
		{name: "gone presence", presence: registry.PresenceGone, lifecycle: nil},
		{name: "end lifecycle", presence: registry.PresenceLive, lifecycle: &endLifecycle},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sess := registry.Session{
				ID:      "s-" + tc.name,
				Harness: registry.Harness("pi"),
				Process: &process,
				Observations: registry.Observations{
					Native: &registry.NativeObservation{
						Event:      "agent_end",
						Lifecycle:  tc.lifecycle,
						Activity:   &running,
						Process:    process,
						ObservedAt: now.Add(-time.Second),
						Reporter:   registry.Reporter{Integration: "pi-extension"},
					},
				},
				Liveness: registry.NewLiveness(tc.presence, registry.ActivityValue(&running), nil),
			}

			exp, err := manage.ExplainSession(context.Background(), sess, manage.ExplainOptions{Now: now})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exp.Hook.Active {
				t.Errorf("%s: Hook.Active = true, want false", tc.name)
			}
			if exp.Hook.FreshnessReason != "integration_ended" {
				t.Errorf("%s: FreshnessReason = %q, want integration_ended", tc.name, exp.Hook.FreshnessReason)
			}
		})
	}
}

func TestExplainDefensiveCopies(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	running := registry.ActivityRunning
	process := registry.ProcessIdentity{PID: 1001, StartIdentity: "boot:1001", Executable: "pi"}
	decision := registry.ActivityDecision{
		Authority: "hook",
		Reason:    "agent_start",
	}

	session := registry.Session{
		ID:       "s-defensive",
		Harness:  registry.Harness("pi"),
		Process:  &process,
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&running), &decision),
	}

	exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mutate original pointers
	process.PID = 9999
	running = registry.ActivityIdle
	decision.Reason = "mutated"

	if exp.Process.PID != 1001 {
		t.Fatalf("explanation process PID mutated: %d, want 1001", exp.Process.PID)
	}
	if *exp.RegistryActivity != registry.ActivityRunning {
		t.Fatalf("explanation registry activity mutated: %q, want running", *exp.RegistryActivity)
	}
	if exp.RegistryDecision.Reason != "agent_start" {
		t.Fatalf("explanation decision reason mutated: %q, want agent_start", exp.RegistryDecision.Reason)
	}
}

func TestExplainJSONCompatibility(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	idle := registry.ActivityIdle
	process := registry.ProcessIdentity{PID: 500, StartIdentity: "boot:500", Executable: "codex"}

	session := registry.Session{
		ID:       "s-json",
		Harness:  registry.Harness("codex"),
		Process:  &process,
		Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%5"},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&idle), nil),
	}

	exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(exp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	expectedKeys := []string{
		"session_id", "harness", "pane_id", "process", "process_match",
		"selected_authority", "final_activity", "hook", "screen", "registry_activity",
	}
	for _, key := range expectedKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing expected json key %q in %+v", key, raw)
		}
	}
}

func TestExplainReadOnlyDoesNotSpawnScreenCapture(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC)
	idle := registry.ActivityIdle
	process := registry.ProcessIdentity{PID: 500, StartIdentity: "boot:500", Executable: "codex"}

	session := registry.Session{
		ID:       "s-readonly",
		Harness:  registry.Harness("codex"),
		Process:  &process,
		Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%999"},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&idle), nil),
	}

	// LiveScreen: false (the default) must succeed without attempting tmux pane capture!
	exp, err := manage.ExplainSession(context.Background(), session, manage.ExplainOptions{Now: now, LiveScreen: false})
	if err != nil {
		t.Fatalf("read-only explain should not fail on non-live pane: %v", err)
	}
	if exp.Screen.Evaluated {
		t.Fatal("read-only explain should not mark Screen.Evaluated as true")
	}
	if exp.Screen.UnavailableReason != "screen_inspection_disabled" {
		t.Fatalf("UnavailableReason = %q, want screen_inspection_disabled", exp.Screen.UnavailableReason)
	}
	if exp.FinalActivity != "idle" {
		t.Fatalf("FinalActivity = %q, want idle", exp.FinalActivity)
	}
}
