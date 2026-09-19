package registry

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSummariesEmptyInputReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	for _, groupBy := range []SummaryGroupBy{
		SummaryGroupByMultiplexerSession,
		SummaryGroupByProject,
		SummaryGroupByHarness,
		"",
	} {
		res := SummariesWithOptions(nil, SummaryOptions{GroupBy: groupBy})
		if len(res) != 0 {
			t.Fatalf("group %q: expected empty slice, got %d items", groupBy, len(res))
		}

		res = SummariesWithOptions([]Session{}, SummaryOptions{GroupBy: groupBy})
		if len(res) != 0 {
			t.Fatalf("group %q: expected empty slice, got %d items", groupBy, len(res))
		}
	}
}

func TestSummariesSumConservationAndNilActivity(t *testing.T) {
	t.Parallel()

	running := ActivityRunning
	idle := ActivityIdle
	failed := ActivityFailed

	sessions := []Session{
		// Live with non-nil activity
		{ID: "1", Presence: PresenceLive, Activity: &running, ProjectRoot: "/p1"},
		{ID: "2", Presence: PresenceLive, Activity: &idle, ProjectRoot: "/p1"},
		// Live with nil activity (must count into ActivityUnknown)
		{ID: "3", Presence: PresenceLive, Activity: nil, ProjectRoot: "/p1"},
		// Gone with nil activity (must NOT increment idle/running or ActivityUnknown)
		{ID: "4", Presence: PresenceGone, Activity: nil, ProjectRoot: "/p1"},
		// Gone with non-nil activity (must NOT increment activity counts)
		{ID: "5", Presence: PresenceGone, Activity: &failed, ProjectRoot: "/p1"},
		// Unknown presence with activity
		{ID: "6", Presence: PresenceUnknown, Activity: &running, ProjectRoot: "/p1"},
		// Unknown presence with nil activity
		{ID: "7", Presence: PresenceUnknown, Activity: nil, ProjectRoot: "/p1"},
		// Blank presence with nil activity (must count as PresenceUnknown, ActivityUnknown)
		{ID: "8", Presence: "", Activity: nil, ProjectRoot: "/p1"},
	}

	summaries := SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByProject})
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	s := summaries[0]

	// Total conservation
	if s.Total != len(sessions) {
		t.Fatalf("Total = %d, want %d", s.Total, len(sessions))
	}
	if s.Live+s.Gone+s.PresenceUnknown != s.Total {
		t.Fatalf("Presence conservation failed: Live(%d) + Gone(%d) + PresUnknown(%d) != Total(%d)",
			s.Live, s.Gone, s.PresenceUnknown, s.Total)
	}
	if s.Live != 3 {
		t.Errorf("Live = %d, want 3", s.Live)
	}
	if s.Gone != 2 {
		t.Errorf("Gone = %d, want 2", s.Gone)
	}
	if s.PresenceUnknown != 3 {
		t.Errorf("PresenceUnknown = %d, want 3", s.PresenceUnknown)
	}

	// Activity conservation over active/unknown presence (Live + PresenceUnknown)
	activeTotal := s.Live + s.PresenceUnknown
	sumActivities := s.Running + s.Waiting + s.Idle + s.Failed + s.Interrupted + s.ActivityUnknown
	if sumActivities != activeTotal {
		t.Fatalf("Activity conservation failed: sum(%d) != activeTotal(%d)", sumActivities, activeTotal)
	}
	if s.Running != 2 {
		t.Errorf("Running = %d, want 2 (1 live, 1 unknown presence)", s.Running)
	}
	if s.Idle != 1 {
		t.Errorf("Idle = %d, want 1", s.Idle)
	}
	if s.Failed != 0 {
		t.Errorf("Failed = %d, want 0 (gone sessions must not contribute to activity)", s.Failed)
	}
	if s.ActivityUnknown != 3 {
		t.Errorf("ActivityUnknown = %d, want 3 (1 live nil, 1 unknown pres nil, 1 blank pres nil)", s.ActivityUnknown)
	}
}

func TestSummariesAllActivitiesAndPresenceStates(t *testing.T) {
	t.Parallel()

	running := ActivityRunning
	waiting := ActivityWaiting
	idle := ActivityIdle
	failed := ActivityFailed
	interrupted := ActivityInterrupted
	unknownAct := ActivityUnknown
	invalidAct := Activity("custom-unknown")

	sessions := []Session{
		{ID: "r", Presence: PresenceLive, Activity: &running, Harness: HarnessClaude},
		{ID: "w", Presence: PresenceLive, Activity: &waiting, Harness: HarnessClaude},
		{ID: "i", Presence: PresenceLive, Activity: &idle, Harness: HarnessClaude},
		{ID: "f", Presence: PresenceLive, Activity: &failed, Harness: HarnessClaude},
		{ID: "int", Presence: PresenceLive, Activity: &interrupted, Harness: HarnessClaude},
		{ID: "unk", Presence: PresenceLive, Activity: &unknownAct, Harness: HarnessClaude},
		{ID: "inv", Presence: PresenceLive, Activity: &invalidAct, Harness: HarnessClaude},
		{ID: "gone", Presence: PresenceGone, Activity: &running, Harness: HarnessClaude},
	}

	summaries := SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByHarness})
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	s := summaries[0]

	if s.Total != 8 {
		t.Errorf("Total = %d, want 8", s.Total)
	}
	if s.Live != 7 {
		t.Errorf("Live = %d, want 7", s.Live)
	}
	if s.Gone != 1 {
		t.Errorf("Gone = %d, want 1", s.Gone)
	}
	if s.Running != 1 {
		t.Errorf("Running = %d, want 1", s.Running)
	}
	if s.Waiting != 1 {
		t.Errorf("Waiting = %d, want 1", s.Waiting)
	}
	if s.Idle != 1 {
		t.Errorf("Idle = %d, want 1", s.Idle)
	}
	if s.Failed != 1 {
		t.Errorf("Failed = %d, want 1", s.Failed)
	}
	if s.Interrupted != 1 {
		t.Errorf("Interrupted = %d, want 1", s.Interrupted)
	}
	if s.ActivityUnknown != 2 {
		t.Errorf("ActivityUnknown = %d, want 2 (unknownAct + invalidAct)", s.ActivityUnknown)
	}
}

func TestSummariesProjectGroupingCollidingBasenames(t *testing.T) {
	t.Parallel()

	running := ActivityRunning

	// Two sessions with identical project basename "service", but distinct roots
	sessions := []Session{
		{ID: "1", Presence: PresenceLive, Activity: &running, ProjectRoot: "/home/alice/work/service"},
		{ID: "2", Presence: PresenceLive, Activity: &running, ProjectRoot: "/home/alice/work/service"},
		{ID: "3", Presence: PresenceLive, Activity: &running, ProjectRoot: "/home/bob/other/service"},
	}

	summaries := SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByProject})
	if len(summaries) != 2 {
		t.Fatalf("expected 2 distinct project groups, got %d", len(summaries))
	}

	// Stable ordering sorts by label then root
	first, second := summaries[0], summaries[1]
	if first.GroupLabel != "service" || second.GroupLabel != "service" {
		t.Fatalf("both labels should be 'service', got %q and %q", first.GroupLabel, second.GroupLabel)
	}
	if first.ProjectRoot != "/home/alice/work/service" {
		t.Errorf("first root = %q, want /home/alice/work/service", first.ProjectRoot)
	}
	if first.Total != 2 {
		t.Errorf("first total = %d, want 2", first.Total)
	}
	if second.ProjectRoot != "/home/bob/other/service" {
		t.Errorf("second root = %q, want /home/bob/other/service", second.ProjectRoot)
	}
	if second.Total != 1 {
		t.Errorf("second total = %d, want 1", second.Total)
	}
}

func TestSummariesProjectGroupingUnknownAndMissingRoot(t *testing.T) {
	t.Parallel()

	running := ActivityRunning

	sessions := []Session{
		// Missing root
		{ID: "1", Presence: PresenceLive, Activity: &running, ProjectRoot: ""},
		{ID: "2", Presence: PresenceLive, Activity: &running, ProjectRoot: ""},
		// Project with root literally named "/var/projects/unknown"
		{ID: "3", Presence: PresenceLive, Activity: &running, ProjectRoot: "/var/projects/unknown"},
		// Normal project
		{ID: "4", Presence: PresenceLive, Activity: &running, ProjectRoot: "/home/user/alpha"},
	}

	summaries := SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByProject})
	if len(summaries) != 3 {
		t.Fatalf("expected 3 distinct groups, got %d", len(summaries))
	}

	// alpha comes first, unknown path comes second, missing root comes last
	if summaries[0].Project != "alpha" || summaries[0].ProjectRoot != "/home/user/alpha" {
		t.Errorf("summary[0] = %+v, want alpha", summaries[0])
	}
	if summaries[1].Project != "unknown" || summaries[1].ProjectRoot != "/var/projects/unknown" {
		t.Errorf("summary[1] = %+v, want literal unknown project", summaries[1])
	}
	if summaries[2].Project != "unknown" || summaries[2].ProjectRoot != "" {
		t.Errorf("summary[2] = %+v, want missing root group", summaries[2])
	}
	if summaries[2].Total != 2 {
		t.Errorf("missing root total = %d, want 2", summaries[2].Total)
	}
}

func TestSummariesHarnessGroupingAndUnknown(t *testing.T) {
	t.Parallel()

	running := ActivityRunning

	sessions := []Session{
		{ID: "1", Presence: PresenceLive, Activity: &running, Harness: HarnessClaude},
		{ID: "2", Presence: PresenceLive, Activity: &running, Harness: HarnessCodex},
		{ID: "3", Presence: PresenceLive, Activity: &running, Harness: ""},
		{ID: "4", Presence: PresenceLive, Activity: &running, Harness: ""},
	}

	summaries := SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByHarness})
	if len(summaries) != 3 {
		t.Fatalf("expected 3 harness groups, got %d", len(summaries))
	}

	if summaries[0].Harness != HarnessClaude || summaries[0].Total != 1 {
		t.Errorf("summary[0] = %+v, want claude", summaries[0])
	}
	if summaries[1].Harness != HarnessCodex || summaries[1].Total != 1 {
		t.Errorf("summary[1] = %+v, want codex", summaries[1])
	}
	if summaries[2].Harness != "" || summaries[2].GroupLabel != "unknown" || summaries[2].Total != 2 {
		t.Errorf("summary[2] = %+v, want unknown harness", summaries[2])
	}
}

func TestSummariesMultiplexerGroupingServerQualified(t *testing.T) {
	t.Parallel()

	running := ActivityRunning

	sessions := []Session{
		{ID: "1", Presence: PresenceLive, Activity: &running, Multiplexer: MultiplexerContext{Kind: MultiplexerTmux, ServerID: "srv1", SessionName: "work"}},
		{ID: "2", Presence: PresenceLive, Activity: &running, Multiplexer: MultiplexerContext{Kind: MultiplexerTmux, ServerID: "srv2", SessionName: "work"}},
		{ID: "3", Presence: PresenceLive, Activity: &running, Multiplexer: MultiplexerContext{Kind: MultiplexerTmux, ServerID: "srv1", SessionName: "work"}},
		{ID: "4", Presence: PresenceLive, Activity: &running}, // unlocated
	}

	summaries := SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByMultiplexerSession})
	if len(summaries) != 3 {
		t.Fatalf("expected 3 mux groups, got %d", len(summaries))
	}

	// srv1:work, srv2:work, then unlocated
	if summaries[0].MultiplexerServerID != "srv1" || summaries[0].MultiplexerSessionName != "work" || summaries[0].Total != 2 {
		t.Errorf("summary[0] = %+v, want srv1:work with total 2", summaries[0])
	}
	if summaries[1].MultiplexerServerID != "srv2" || summaries[1].MultiplexerSessionName != "work" || summaries[1].Total != 1 {
		t.Errorf("summary[1] = %+v, want srv2:work with total 1", summaries[1])
	}
	if summaries[2].GroupLabel != "unknown" || summaries[2].Total != 1 {
		t.Errorf("summary[2] = %+v, want unlocated with total 1", summaries[2])
	}
}

func TestSummariesFiltersBeforeGrouping(t *testing.T) {
	t.Parallel()

	running := ActivityRunning
	idle := ActivityIdle

	sessions := []Session{
		{ID: "1", Presence: PresenceLive, Activity: &running, Harness: HarnessClaude, ProjectRoot: "/app"},
		{ID: "2", Presence: PresenceLive, Activity: &idle, Harness: HarnessCodex, ProjectRoot: "/app"},
		{ID: "3", Presence: PresenceGone, Activity: nil, Harness: HarnessClaude, ProjectRoot: "/app"},
	}

	// Filter by HarnessClaude before summarizing
	filtered := filterSessions(sessions, Filter{Harness: HarnessClaude})
	summaries := SummariesWithOptions(filtered, SummaryOptions{GroupBy: SummaryGroupByProject})

	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].Total != 2 {
		t.Errorf("total = %d, want 2 (only claude sessions)", summaries[0].Total)
	}
	if summaries[0].Live != 1 || summaries[0].Gone != 1 {
		t.Errorf("Live = %d, Gone = %d, want 1 live and 1 gone", summaries[0].Live, summaries[0].Gone)
	}
}

func TestSummariesFileAndMemoryStoreParity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	storePath := filepath.Join(dir, "sessions.json")
	fileStore := NewFileStore(storePath)
	memStore, err := OpenMemoryStore(storePath)
	if err != nil {
		t.Fatal(err)
	}

	running := ActivityRunning
	obs1 := Observation{
		Source:   ObservationSourceNative,
		Evidence: ObservationEvidenceNativeEvent,
		Harness:  HarnessClaude,
		Identity: ObservationIdentity{SessionID: "sess-1"},
		Presence: new(PresenceLive),
		Activity: &running,
		Catalog:  &CatalogMetadata{ProjectRoot: "/repo/one"},
		Tmux:     &TmuxContext{SessionName: "main"},
	}
	obs2 := Observation{
		Source:   ObservationSourceNative,
		Evidence: ObservationEvidenceNativeEvent,
		Harness:  HarnessCodex,
		Identity: ObservationIdentity{SessionID: "sess-2"},
		Presence: new(PresenceLive),
		Activity: &running,
		Catalog:  &CatalogMetadata{ProjectRoot: "/repo/two"},
		Tmux:     &TmuxContext{SessionName: "other"},
	}

	ctx := t.Context()
	if _, err := fileStore.ObserveBatch(ctx, []Observation{obs1, obs2}); err != nil {
		t.Fatal(err)
	}
	if _, err := memStore.ObserveBatch(ctx, []Observation{obs1, obs2}); err != nil {
		t.Fatal(err)
	}

	for _, groupBy := range []SummaryGroupBy{
		SummaryGroupByMultiplexerSession,
		SummaryGroupByProject,
		SummaryGroupByHarness,
	} {
		opts := SummaryOptions{GroupBy: groupBy}
		fileSum, err := fileStore.SummaryWithOptions(ctx, Filter{}, opts)
		if err != nil {
			t.Fatalf("fileStore summary error for %s: %v", groupBy, err)
		}
		memSum, err := memStore.SummaryWithOptions(ctx, Filter{}, opts)
		if err != nil {
			t.Fatalf("memStore summary error for %s: %v", groupBy, err)
		}

		assertSummaryParity(t, groupBy, fileSum, memSum)
	}
}

func assertSummaryParity(t *testing.T, groupBy SummaryGroupBy, fileSum, memSum []Summary) {
	t.Helper()
	if len(fileSum) != len(memSum) {
		t.Fatalf("group %s: fileStore length %d != memStore length %d", groupBy, len(fileSum), len(memSum))
	}
	for i := range fileSum {
		if fileSum[i].GroupKey != memSum[i].GroupKey ||
			fileSum[i].GroupLabel != memSum[i].GroupLabel ||
			fileSum[i].Total != memSum[i].Total ||
			fileSum[i].Live != memSum[i].Live {
			t.Fatalf("group %s item %d mismatch: file=%+v, mem=%+v", groupBy, i, fileSum[i], memSum[i])
		}
	}
}

func TestSummariesUnsupportedGroupBy(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))

	_, err := store.SummaryWithOptions(ctx, Filter{}, SummaryOptions{GroupBy: "invalid-group"})
	if !errors.Is(err, ErrUnsupportedGroupBy) {
		t.Fatalf("expected ErrUnsupportedGroupBy, got %v", err)
	}

	memStore, err := OpenMemoryStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = memStore.SummaryWithOptions(ctx, Filter{}, SummaryOptions{GroupBy: "invalid-group"})
	if !errors.Is(err, ErrUnsupportedGroupBy) {
		t.Fatalf("expected ErrUnsupportedGroupBy, got %v", err)
	}
}
