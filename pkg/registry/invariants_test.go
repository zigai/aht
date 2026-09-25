package registry

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/registrycheck/core"
)

var errExclusiveProcess = errors.New("exclusive process conflict")

func checkInvariantSessions(sessions []Session) error {
	projected := make([]core.Session, len(sessions))
	for index, session := range sessions {
		var activity string
		if value := session.Activity(); value != nil {
			activity = string(*value)
		}
		projected[index] = core.Session{
			ID: session.ID, Harness: string(session.Harness), CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
			HasLiveness: session.Liveness != nil, Presence: string(session.Presence()), Activity: activity,
			ValidState:  session.Presence().IsValid() && (session.Activity() == nil || session.Activity().IsValid()),
			NativeEnded: session.Observations.Native != nil && session.Observations.Native.Lifecycle != nil && *session.Observations.Native.Lifecycle == NativeLifecycleEnd,
		}
	}
	if err := core.Sessions(projected); err != nil {
		return fmt.Errorf("registry sessions: %w", err)
	}
	return nil
}

func assertGCOnlyExpiredGone(t *testing.T, before, after []Session, now time.Time, deleteAfter time.Duration) {
	t.Helper()
	remaining := make(map[string]struct{}, len(after))
	for _, session := range after {
		remaining[session.ID] = struct{}{}
	}
	for _, session := range before {
		if _, ok := remaining[session.ID]; ok {
			continue
		}
		if deleteAfter < 0 || session.Presence() != PresenceGone || session.PresenceChangedAt.IsZero() || now.Sub(session.PresenceChangedAt) < deleteAfter {
			t.Fatalf("gc removed unexpired session %q, presence=%s, changed=%s, deleteAfter=%s", session.ID, session.Presence(), session.PresenceChangedAt, deleteAfter)
		}
	}
}

func assertExclusiveProcesses(t *testing.T, sessions []Session) {
	t.Helper()
	// matching.go retires conflicting native identities attached to one process.
	if err := exclusiveProcessConflict(sessions); err != nil {
		t.Fatal(err)
	}
}

func exclusiveProcessConflict(sessions []Session) error {
	seen := make(map[string]Session)
	for _, session := range sessions {
		if !(fixtureRules{}).Policy(session.Harness).ExclusiveProcess || session.Presence() != PresenceLive || session.Process == nil || !session.Process.Complete() || session.Observations.Native == nil || !session.Observations.Native.Process.Equal(*session.Process) {
			continue
		}
		if native := session.Observations.Native; native != nil && native.Reporter.MultiSession {
			continue
		}
		key := fmt.Sprintf("%s/%d/%s", session.Harness, session.Process.PID, session.Process.StartIdentity)
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("%w: %s has live sessions %q (%s, native %s, native PID %d, process %s) and %q (%s, native %s, native PID %d, process %s)", errExclusiveProcess, key, previous.ID, previous.Presence(), previous.Observations.Native.ObservedAt, previous.Observations.Native.Process.PID, previous.Incarnation.ObservedAt, session.ID, session.Presence(), session.Observations.Native.ObservedAt, session.Observations.Native.Process.PID, session.Incarnation.ObservedAt)
		}
		seen[key] = session
	}
	return nil
}

func TestReducerDeterminismAndRejectedSequence(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	running, idle := ActivityRunning, ActivityIdle
	first := uint64(2)
	stale := uint64(1)
	start := Observation{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: "invariant-sequence"}, Evidence: &Report{Reporter: Reporter{Integration: "parity-test", Sequence: &first}, Activity: &running}}
	reducer := NewReducer(fixtureRules{})
	initial := State{Sessions: make(map[string]Session)}
	state, _, err := reducer.Apply(initial, []Observation{start}, base)
	if err != nil {
		t.Fatal(err)
	}
	state2, _, err := reducer.Apply(initial, []Observation{start}, base)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(state, state2, cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y })); diff != "" {
		t.Fatalf("same prior state, batch and time differed (-first +second):\n%s", diff)
	}
	staleReport := start
	staleReport.At = base.Add(time.Second)
	staleReport.Evidence = &Report{Reporter: Reporter{Integration: "parity-test", Sequence: &stale}, Activity: &idle}
	rejected, _, err := reducer.Apply(state, []Observation{staleReport}, base.Add(time.Second))
	if !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("stale sequence error = %v", err)
	}
	if diff := cmp.Diff(state, rejected, cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y })); diff != "" {
		t.Fatalf("stale sequence changed state (-before +after):\n%s", diff)
	}
}

func TestJournalReplayGuardAndGCInvariant(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "state.json")
	journal := NewJournal(path, fixtureRules{})
	clock := base.Add(time.Hour)
	journal.setNowForTest(func() time.Time { return clock })
	end := NativeLifecycleEnd
	for _, item := range []struct {
		id        string
		lifecycle *NativeLifecycle
	}{{"gone", &end}, {"present", nil}} {
		_, err := journal.Observe(t.Context(), Observation{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: item.id}, Evidence: &Report{Event: "state", Lifecycle: item.lifecycle}})
		if err != nil {
			t.Fatal(err)
		}
	}
	before := mustListParity(t, journal)
	snap, err := journal.loadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	entry := journalEntry{Sequence: snap.JournalSequence, ReceivedAt: clock, Reset: true}
	if err := NewReducer(fixtureRules{}).foldJournal(context.Background(), &snap, []journalEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(before, FilterSessions(mapSessions(snap.Sessions), Filter{}), cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y })); diff != "" {
		t.Fatalf("replayed entry changed snapshot (-before +after):\n%s", diff)
	}
	result, err := journal.GC(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	after := mustListParity(t, journal)
	assertGCOnlyExpiredGone(t, before, after, clock, 0)
	if result.Deleted != 1 {
		t.Fatalf("gc deleted %d, want 1", result.Deleted)
	}
}

func mapSessions(sessions map[string]Session) []Session {
	result := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, session)
	}
	return result
}

func TestExclusiveProcessAndActivityAuthorityInvariant(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	process := ProcessIdentity{PID: 444, StartIdentity: "boot:444", Executable: "agent"}
	state := State{Sessions: make(map[string]Session)}
	reducer := NewReducer(fixtureRules{})
	for index, id := range []string{"first", "second"} {
		activity := ActivityRunning
		observation := Observation{Harness: HarnessPi, At: base.Add(time.Duration(index) * time.Second), Subject: ObservationIdentity{SessionID: id}, Evidence: &Report{Activity: &activity, Process: &process}}
		var err error
		state, _, err = reducer.Apply(state, []Observation{observation}, base.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	sessions := mapSessions(state.Sessions)
	assertExclusiveProcesses(t, sessions)
	var retired bool
	for _, session := range sessions {
		if session.SessionID == "first" && session.Presence() == PresenceGone {
			retired = true
		}
	}
	if !retired {
		t.Fatal("conflicting process session was not retired")
	}
	for _, session := range sessions {
		authority, _ := ActivityAuthority(session, fixtureRules{}.Policy(session.Harness), base.Add(time.Minute))
		if authority != AuthorityScreen {
			t.Fatalf("activity authority for session %q = %s, want screen after hook lease", session.ID, authority)
		}
	}
}

func TestActivityDecisionFollowsActivityAuthority(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	process := ProcessIdentity{PID: 777, StartIdentity: "boot:777", Executable: "agent"}
	running, live := ActivityRunning, PresenceLive
	state := State{Sessions: make(map[string]Session)}
	rules := fixtureRules{}
	reducer := NewReducer(rules)
	report := Observation{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: "authority"}, Evidence: &Report{Event: "agent_start", Reporter: Reporter{Integration: rules.Policy(HarnessPi).Reporter}, Activity: &running, Claim: &live, Process: &process}}
	var err error
	state, _, err = reducer.Apply(state, []Observation{report}, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{base.Add(time.Second), base.Add(time.Minute)} {
		reading := Observation{Harness: HarnessPi, At: at, Subject: ObservationIdentity{SessionID: "authority"}, Evidence: (*Reading)(&ScreenObservation{Activity: ActivityWaiting, Authority: AuthorityScreen, Reason: "rule", Process: process, ObservedAt: at})}
		state, _, err = reducer.Apply(state, []Observation{reading}, at)
		if err != nil {
			t.Fatal(err)
		}
		session := mapSessions(state.Sessions)[0]
		authority, _ := ActivityAuthority(session, rules.Policy(session.Harness), at)
		if decision := session.Decision(); decision == nil || decision.Authority != authority {
			t.Fatalf("at %s decision = %v, authority = %s", at, decision, authority)
		}
		if authority == AuthorityScreen && (session.Activity() == nil || *session.Activity() != ActivityWaiting) {
			t.Fatalf("screen authority did not apply waiting activity: %+v", session)
		}
	}
}

//nolint:cyclop,gocognit // The regression checks both sighting identities and native resume from one tombstone.
func TestSightingCannotReviveRetiredExclusiveSession(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	process := ProcessIdentity{PID: 888, StartIdentity: "boot:888", Executable: "agent"}
	running, live := ActivityRunning, PresenceLive
	state := State{Sessions: make(map[string]Session)}
	reducer := NewReducer(fixtureRules{authority: AuthorityScreen})
	for index, id := range []string{"old", "new"} {
		at := base.Add(time.Duration(index) * time.Second)
		observation := Observation{Harness: HarnessPi, At: at, Subject: ObservationIdentity{SessionID: id}, Evidence: &Report{Event: "start", Claim: &live, Activity: &running, Process: &process}}
		var err error
		state, _, err = reducer.Apply(state, []Observation{observation}, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := exclusiveProcessConflict(mapSessions(state.Sessions)); err != nil {
		t.Fatalf("native replacement did not retire old session: %v", err)
	}
	absentAt := base.Add(1500 * time.Millisecond)
	absent := Observation{Harness: HarnessPi, At: absentAt, Subject: ObservationIdentity{SessionID: "old"}, Evidence: &Sighting{Process: process, Present: false}}
	state, _, err := reducer.Apply(state, []Observation{absent}, absentAt)
	if err != nil {
		t.Fatal(err)
	}
	at := base.Add(2 * time.Second)
	sameIdentity := process
	sameIdentity.Executable = "different-path"
	sighting := Observation{Harness: HarnessPi, At: at, Subject: ObservationIdentity{SessionID: "old"}, Evidence: &Sighting{Process: sameIdentity, Present: true}}
	state, _, err = reducer.Apply(state, []Observation{sighting}, at)
	if err != nil {
		t.Fatal(err)
	}
	assertExclusiveProcesses(t, mapSessions(state.Sessions))
	for _, session := range state.Sessions {
		if session.SessionID == "old" {
			if session.Presence() != PresenceGone {
				t.Fatalf("retired session became %s after same-process sighting", session.Presence())
			}
			if decision := session.Decision(); decision == nil || decision.Reason != sessionReplacedReason || !decision.Process.Equal(process) {
				t.Fatalf("replacement decision = %+v, want replacing process", decision)
			}
		}
	}
	newProcess := ProcessIdentity{PID: 889, StartIdentity: "boot:889", Executable: "agent"}
	newAt := base.Add(3 * time.Second)
	newSighting := Observation{Harness: HarnessPi, At: newAt, Subject: ObservationIdentity{SessionID: "old"}, Evidence: &Sighting{Process: newProcess, Present: true}}
	newState, _, err := reducer.Apply(state, []Observation{newSighting}, newAt)
	if err != nil {
		t.Fatal(err)
	}
	assertExclusiveProcesses(t, mapSessions(newState.Sessions))
	var revived bool
	for _, session := range newState.Sessions {
		if session.SessionID == "old" && session.Presence() == PresenceLive && session.Process != nil && session.Process.Equal(newProcess) {
			revived = true
		}
	}
	if !revived {
		t.Fatal("different-process sighting did not revive replaced session")
	}
	resume := NativeLifecycleResume
	resumeAt := base.Add(3 * time.Second)
	observation := Observation{Harness: HarnessPi, At: resumeAt, Subject: ObservationIdentity{SessionID: "old"}, Evidence: &Report{Event: "resume", Lifecycle: &resume, Claim: &live, Activity: &running, Process: &process}}
	state, _, err = reducer.Apply(state, []Observation{observation}, resumeAt)
	if err != nil {
		t.Fatal(err)
	}
	assertExclusiveProcesses(t, mapSessions(state.Sessions))
	var resumed bool
	for _, session := range state.Sessions {
		if session.SessionID == "old" && session.Presence() == PresenceLive {
			resumed = true
		}
	}
	if !resumed {
		t.Fatal("native resume did not restore old session")
	}
}
