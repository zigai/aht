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

func TestPresenceVerificationInvariants(t *testing.T) {
	t.Parallel()

	coverage := core.NewCoverage()

	for _, test := range []struct {
		name    string
		before  Presence
		check   Presence
		process *ProcessIdentity
		delayed bool
		want    Presence
	}{
		{name: "unverified owner", before: PresenceLive, check: PresenceUnknown, want: PresenceUnknown},
		{name: "gone location", before: PresenceLive, check: PresenceGone, want: PresenceGone},
		{
			name:   "running owner",
			before: PresenceLive,
			check:  PresenceGone,
			process: &ProcessIdentity{
				PID:           91,
				StartIdentity: "boot:91",
			},
			want: PresenceLive,
		},
		{name: "ended session", before: PresenceGone, check: PresenceUnknown, want: PresenceGone},
		{name: "delayed verification", before: PresenceLive, check: PresenceGone, delayed: true, want: PresenceLive},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
			at := time.Now().UTC().Add(-time.Minute)

			before, err := store.Observe(
				t.Context(),
				Observation{
					Harness: HarnessPi,
					At:      at,
					Subject: ObservationIdentity{SessionID: "native-session"},
					Evidence: &Report{
						Event:   "native.status",
						Claim:   &test.before,
						Process: test.process,
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			checkAt := at.Add(time.Second)
			if test.delayed {
				checkAt = at.Add(-time.Second)
			}

			_, err = store.Observe(
				t.Context(),
				Observation{
					Harness:  HarnessPi,
					At:       checkAt,
					Subject:  ObservationIdentity{SessionID: "native-session"},
					Evidence: &Verification{Presence: test.check, Reason: "owner_check"},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			after, err := store.Get(t.Context(), before.ID)
			if err != nil {
				t.Fatal(err)
			}

			if after.Presence() != test.want {
				t.Fatalf("presence = %s, want %s", after.Presence(), test.want)
			}

			if err := core.Verification(
				string(before.Presence()),
				string(after.Presence()),
				before.Process != nil && before.Process.Complete(),
			); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(
				before.Observations.Native,
				after.Observations.Native,
				cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y }),
			); diff != "" {
				t.Fatalf("verification changes native report (-before +after):\n%s", diff)
			}

			coverage.Hit(test.name)
		})
	}

	if missing := coverage.Missing(
		"unverified owner",
		"gone location",
		"running owner",
		"ended session",
		"delayed verification",
	); len(
		missing,
	) > 0 {
		t.Fatalf("verification situations not covered: %v", missing)
	}
}

func checkInvariantSessions(sessions []Session) error {
	projected := projectInvariantSessions(sessions)

	if err := core.Sessions(projected); err != nil {
		return fmt.Errorf("registry sessions: %w", err)
	}

	return nil
}

func assertGCOnlyExpiredGone(
	t *testing.T,
	before []Session, after []Session,
	now time.Time,
	deleteAfter time.Duration,
) {
	t.Helper()

	remaining := make(map[string]struct{}, len(after))
	for _, session := range after {
		remaining[session.ID] = struct{}{}
	}

	for _, session := range before {
		if _, ok := remaining[session.ID]; ok {
			continue
		}

		if deleteAfter < 0 || session.Presence() != PresenceGone ||
			session.PresenceChangedAt.IsZero() ||
			now.Sub(session.PresenceChangedAt) < deleteAfter {
			t.Fatalf(
				"gc removed unexpired session %q, presence=%s, changed=%s, deleteAfter=%s",
				session.ID,
				session.Presence(),
				session.PresenceChangedAt,
				deleteAfter,
			)
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
		if !(fixtureRules{}).Policy(session.Harness).ExclusiveProcess ||
			session.Presence() != PresenceLive ||
			session.Process == nil ||
			!session.Process.Complete() ||
			session.Observations.Native == nil ||
			!session.Observations.Native.Process.Equal(*session.Process) {
			continue
		}

		if native := session.Observations.Native; native != nil && native.Reporter.MultiSession {
			continue
		}

		key := fmt.Sprintf(
			"%s/%d/%s",
			session.Harness,
			session.Process.PID,
			session.Process.StartIdentity,
		)
		if previous, exists := seen[key]; exists {
			return fmt.Errorf(
				"%w: %s has live sessions %q (%s, native %s, native PID %d, "+
					"process %s) and %q (%s, native %s, native PID %d, process "+
					"%s)",
				errExclusiveProcess,
				key,
				previous.ID,
				previous.Presence(),
				previous.Observations.Native.ObservedAt,
				previous.Observations.Native.Process.PID,
				previous.Incarnation.EvidenceAt,
				session.ID,
				session.Presence(),
				session.Observations.Native.ObservedAt,
				session.Observations.Native.Process.PID,
				session.Incarnation.EvidenceAt,
			)
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
	start := Observation{
		Harness: HarnessPi,
		At:      base,
		Subject: ObservationIdentity{SessionID: "invariant-sequence"},
		Evidence: &Report{
			Reporter: Reporter{Integration: "parity-test", Sequence: &first},
			Activity: &running,
		},
	}
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

	if diff := cmp.Diff(
		state,
		state2,
		cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y }),
	); diff != "" {
		t.Fatalf("same prior state, batch and time differed (-first +second):\n%s", diff)
	}

	staleReport := start
	staleReport.At = base.Add(time.Second)
	staleReport.Evidence = &Report{
		Reporter: Reporter{Integration: "parity-test", Sequence: &stale},
		Activity: &idle,
	}

	rejected, _, err := reducer.Apply(state, []Observation{staleReport}, base.Add(time.Second))
	if !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("stale sequence error = %v", err)
	}

	if diff := cmp.Diff(
		state,
		rejected,
		cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y }),
	); diff != "" {
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
		_, err := journal.Observe(
			t.Context(),
			Observation{
				Harness:  HarnessPi,
				At:       base,
				Subject:  ObservationIdentity{SessionID: item.id},
				Evidence: &Report{Event: "state", Lifecycle: item.lifecycle},
			},
		)
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
	if err := NewReducer(
		fixtureRules{},
	).foldJournal(context.Background(), &snap, []decodedJournalEntry{{journalEntry: entry}}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(
		before,
		FilterSessions(mapSessions(snap.Sessions), Filter{}),
		cmp.Comparer(func(x, y ProcessIdentity) bool { return x == y }),
	); diff != "" {
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
		observation := Observation{
			Harness:  HarnessPi,
			At:       base.Add(time.Duration(index) * time.Second),
			Subject:  ObservationIdentity{SessionID: id},
			Evidence: &Report{Activity: &activity, Process: &process},
		}

		var err error

		state, _, err = reducer.Apply(
			state,
			[]Observation{observation},
			base.Add(time.Duration(index)*time.Second),
		)
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
		authority, _ := ActivityAuthority(
			session,
			fixtureRules{}.Policy(session.Harness),
			base.Add(time.Minute),
		)
		if authority != AuthorityScreen {
			t.Fatalf(
				"activity authority for session %q = %s, want screen after hook lease",
				session.ID,
				authority,
			)
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
	report := Observation{
		Harness: HarnessPi,
		At:      base,
		Subject: ObservationIdentity{SessionID: "authority"},
		Evidence: &Report{
			Event:    "agent_start",
			Reporter: Reporter{Integration: rules.Policy(HarnessPi).Reporter},
			Activity: &running,
			Claim:    &live,
			Process:  &process,
		},
	}

	var err error

	state, _, err = reducer.Apply(state, []Observation{report}, base)
	if err != nil {
		t.Fatal(err)
	}

	for _, at := range []time.Time{base.Add(time.Second), base.Add(time.Minute)} {
		reading := Observation{
			Harness: HarnessPi,
			At:      at,
			Subject: ObservationIdentity{SessionID: "authority"},
			Evidence: (*Reading)(
				&ScreenObservation{
					Activity:   ActivityWaiting,
					Authority:  AuthorityScreen,
					Reason:     "rule",
					Process:    process,
					ObservedAt: at,
				},
			),
		}

		state, _, err = reducer.Apply(state, []Observation{reading}, at)
		if err != nil {
			t.Fatal(err)
		}

		session := mapSessions(state.Sessions)[0]

		authority, _ := ActivityAuthority(session, rules.Policy(session.Harness), at)
		if decision := session.Decision(); decision == nil || decision.Authority != authority {
			t.Fatalf("at %s decision = %v, authority = %s", at, decision, authority)
		}

		if authority == AuthorityScreen &&
			(session.Activity() == nil || *session.Activity() != ActivityWaiting) {
			t.Fatalf("screen authority did not apply waiting activity: %+v", session)
		}
	}
}

func TestSightingCannotReviveRetiredExclusiveSession(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	process := ProcessIdentity{PID: 888, StartIdentity: "boot:888", Executable: "agent"}
	running, live := ActivityRunning, PresenceLive
	state := State{Sessions: make(map[string]Session)}
	reducer := NewReducer(fixtureRules{authority: AuthorityScreen})

	for index, id := range []string{"old", "new"} {
		at := base.Add(time.Duration(index) * time.Second)
		observation := Observation{
			Harness:  HarnessPi,
			At:       at,
			Subject:  ObservationIdentity{SessionID: id},
			Evidence: &Report{Event: "start", Claim: &live, Activity: &running, Process: &process},
		}

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
	absent := Observation{
		Harness:  HarnessPi,
		At:       absentAt,
		Subject:  ObservationIdentity{SessionID: "old"},
		Evidence: &Sighting{Process: process, Present: false},
	}

	state, _, err := reducer.Apply(state, []Observation{absent}, absentAt)
	if err != nil {
		t.Fatal(err)
	}

	at := base.Add(2 * time.Second)
	sameIdentity := process
	sameIdentity.Executable = "different-path"
	sighting := Observation{
		Harness:  HarnessPi,
		At:       at,
		Subject:  ObservationIdentity{SessionID: "old"},
		Evidence: &Sighting{Process: sameIdentity, Present: true},
	}

	state, _, err = reducer.Apply(state, []Observation{sighting}, at)
	if err != nil {
		t.Fatal(err)
	}

	assertExclusiveProcesses(t, mapSessions(state.Sessions))

	for _, session := range state.Sessions {
		if session.SessionID == "old" {
			if session.Presence() != PresenceGone {
				t.Fatalf(
					"retired session became %s after same-process sighting",
					session.Presence(),
				)
			}

			if decision := session.Decision(); decision == nil ||
				decision.Reason != sessionReplacedReason ||
				!decision.Process.Equal(process) {
				t.Fatalf("replacement decision = %+v, want replacing process", decision)
			}
		}
	}

	newProcess := ProcessIdentity{PID: 889, StartIdentity: "boot:889", Executable: "agent"}
	newAt := base.Add(3 * time.Second)
	newSighting := Observation{
		Harness:  HarnessPi,
		At:       newAt,
		Subject:  ObservationIdentity{SessionID: "old"},
		Evidence: &Sighting{Process: newProcess, Present: true},
	}

	newState, _, err := reducer.Apply(state, []Observation{newSighting}, newAt)
	if err != nil {
		t.Fatal(err)
	}

	assertExclusiveProcesses(t, mapSessions(newState.Sessions))

	var revived bool

	for _, session := range newState.Sessions {
		if session.SessionID == "old" && session.Presence() == PresenceLive &&
			session.Process != nil &&
			session.Process.Equal(newProcess) {
			revived = true
		}
	}

	if !revived {
		t.Fatal("different-process sighting did not revive replaced session")
	}

	resume := NativeLifecycleResume
	resumeAt := base.Add(3 * time.Second)
	observation := Observation{
		Harness: HarnessPi,
		At:      resumeAt,
		Subject: ObservationIdentity{SessionID: "old"},
		Evidence: &Report{
			Event:     "resume",
			Lifecycle: &resume,
			Claim:     &live,
			Activity:  &running,
			Process:   &process,
		},
	}

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

func projectInvariantSessions(sessions []Session) []core.Session {
	projected := make([]core.Session, len(sessions))
	for index, session := range sessions {
		var activity string
		if value := session.Activity(); value != nil {
			activity = string(*value)
		}

		var process string
		if session.Process != nil {
			process = core.ProcessKey(session.Process.PID, session.Process.StartIdentity)
		}

		var decisionAt time.Time
		if decision := session.Decision(); decision != nil {
			decisionAt = decision.ObservedAt
		}

		var previousProcess string
		if session.Incarnation.Previous != nil {
			previousProcess = core.ProcessKey(
				session.Incarnation.Previous.PID,
				session.Incarnation.Previous.StartIdentity,
			)
		}

		projected[index] = core.Session{
			TransitionAt:    session.Incarnation.TransitionAt,
			PreviousProcess: previousProcess,
			SessionID:       session.SessionID,
			SessionPath:     session.SessionPath,
			Process:         process,
			IncarnationProcess: core.ProcessKey(
				session.Incarnation.Process.PID,
				session.Incarnation.Process.StartIdentity,
			),
			Incarnation: string(
				session.Incarnation.State,
			),
			EvidenceAt:  session.Incarnation.EvidenceAt,
			DecisionAt:  decisionAt,
			ID:          session.ID,
			Harness:     string(session.Harness),
			CreatedAt:   session.CreatedAt,
			UpdatedAt:   session.UpdatedAt,
			HasLiveness: session.Liveness != nil,
			Presence:    string(session.Presence()),
			Activity:    activity,
			ValidState: session.Presence().Valid() &&
				(session.Activity() == nil || session.Activity().Valid()),
			NativeEnded: session.Observations.Native != nil &&
				session.Observations.Native.Lifecycle != nil &&
				*session.Observations.Native.Lifecycle == NativeLifecycleEnd,
		}
	}

	return projected
}

func TestIncarnationTransitionInvariants(t *testing.T) {
	t.Parallel()

	coverage := core.NewCoverage()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first := ProcessIdentity{PID: 901, StartIdentity: "boot:901"}
	second := ProcessIdentity{PID: 901, StartIdentity: "boot:902"}
	report := func(at time.Time, process *ProcessIdentity, lifecycle *NativeLifecycle, sequence uint64) Observation {
		result := Observation{
			Harness: HarnessPi,
			At:      at,
			Subject: ObservationIdentity{SessionID: "incarnation"},
			Evidence: &Report{
				Reporter: Reporter{
					Integration: "pi-extension",
					Sequence:    new(sequence),
				},
				Activity:  new(ActivityRunning),
				Claim:     new(PresenceLive),
				Process:   process,
				Lifecycle: lifecycle,
			},
		}
		if lifecycle != nil && *lifecycle == NativeLifecycleEnd {
			result.Report().Activity = nil
			result.Report().Claim = new(PresenceGone)
		}

		return result
	}
	sighting := func(at time.Time, process ProcessIdentity, present bool) Observation {
		return Observation{
			Harness:  HarnessPi,
			At:       at,
			Evidence: &Sighting{Process: process, Present: present},
		}
	}

	for _, situation := range []string{
		"delayed end",
		"delayed absence",
		"resume before scan",
		"future report process exit",
		"identity tombstone merge",
		"ordering drop atomic matching",
		"terminal time monotonic",
		"replaced session end isolated",
		"equivalent sequence replay",
		"sequence content conflict",
		"stale terminal sequence conflict",
	} {
		t.Run(situation, func(t *testing.T) {
			state := State{Sessions: map[string]Session{}}
			apply := func(received time.Time, observations ...Observation) Session {
				t.Helper()

				for _, observation := range observations {
					before := state

					var err error

					state, _, err = NewReducer(
						fixtureRules{},
					).Apply(state, []Observation{observation}, received)
					if err != nil {
						t.Fatal(err)
					}

					if err := checkInvariantSessions(mapSessions(state.Sessions)); err != nil {
						t.Fatal(err)
					}

					for id, prior := range before.Sessions {
						if current, ok := state.Sessions[id]; ok {
							projected := projectInvariantSessions([]Session{prior, current})

							process := ""
							if value := observation.ProcessIdentity(); value != nil {
								process = core.ProcessKey(value.PID, value.StartIdentity)
							}

							ends := observation.Kind() == "report" &&
								reportEnds(observation.Report())
							if err := core.Transition(projected[0], projected[1], core.Event{
								Kind:        observation.Kind(),
								Process:     process,
								Harness:     string(observation.Harness),
								SessionID:   observation.Subject.SessionID,
								SessionPath: observation.Subject.SessionPath,
								At:          observationTime(observation.At, received),
								Ends:        ends,
								Present:     observation.Present() != nil && *observation.Present(),
							}); err != nil {
								t.Fatal(err)
							}
						}
					}
				}

				for _, session := range state.Sessions {
					if session.SessionID == "incarnation" {
						return session
					}
				}

				return Session{}
			}

			initial := report(base, &first, nil, 90)
			if situation == "replaced session end isolated" {
				initial.Report().Lifecycle = new(NativeLifecycleStart)
			}

			if situation == "identity tombstone merge" {
				initial = report(base, nil, new(NativeLifecycleEnd), 90)
			}

			apply(base, initial)
			scan := base.Add(10 * time.Second)

			var current Session

			switch situation {
			case "replaced session end isolated":
				replacement := report(base.Add(time.Second), &first, new(NativeLifecycleStart), 1)
				replacement.Subject.SessionID = "replacement"
				apply(scan, replacement)

				var beforeReplacement Session

				for _, session := range state.Sessions {
					if session.SessionID == "replacement" {
						beforeReplacement = cloneSessionValue(session)
					}
				}

				current = apply(
					scan,
					report(base.Add(2*time.Second), &first, new(NativeLifecycleEnd), 91),
				)
				if current.Presence() != PresenceGone || current.Activity() != nil {
					t.Fatal("replaced session end revived retired identity")
				}

				var liveReplacement bool

				for _, session := range state.Sessions {
					if session.SessionID == "replacement" {
						liveReplacement = session.Presence() == PresenceLive &&
							ActivityValue(session.Activity()) == ActivityRunning
						if diff := cmp.Diff(beforeReplacement, session); diff != "" {
							t.Fatalf(
								"replaced session end mutated replacement (-before +after): %s",
								diff,
							)
						}
					}
				}

				if !liveReplacement {
					t.Fatal("replaced session end terminated replacement")
				}
			case "delayed end", "delayed absence":
				prior := cloneSessionValue(apply(scan, sighting(scan, first, true)))
				terminal := func(at time.Time, sequence uint64) Observation {
					if situation == "delayed absence" {
						return sighting(at, first, false)
					}

					return report(at, &first, new(NativeLifecycleEnd), sequence)
				}

				stale := terminal(base.Add(time.Second), 91)
				stale.Subject.CWD = "/stale"

				current = apply(scan, stale)
				if diff := cmp.Diff(prior, current); diff != "" {
					t.Fatalf("stale terminal evidence changed session: %s", diff)
				}

				coverage.Hit("stale own-process terminal no-op")

				for index, at := range []time.Time{scan, scan.Add(time.Second)} {
					current = apply(at, terminal(at, uint64(91+index)))
					if current.Presence() != PresenceGone || current.Activity() != nil {
						t.Fatal("current terminal evidence did not terminate")
					}
				}

				coverage.Hit("equal own-process terminal")
				coverage.Hit("fresh own-process terminal")
				coverage.Hit("terminal Gone no activity")
			case "resume before scan":
				apply(scan, sighting(scan, first, false), sighting(scan, second, true))

				current = apply(
					scan,
					report(base.Add(9*time.Second), &second, new(NativeLifecycleResume), 1),
				)
				if current.Presence() != PresenceLive || !current.Process.Equal(second) ||
					*current.Observations.Native.Reporter.Sequence != 1 {
					t.Fatal("resume or fresh sequence baseline lost")
				}

				coverage.Hit("sequence baseline")

				prior := cloneSessionValue(current)

				current = apply(
					scan.Add(time.Second),
					report(scan.Add(time.Second), &first, new(NativeLifecycleStart), 100),
				)
				if diff := cmp.Diff(prior, current); diff != "" {
					t.Fatalf("retired native start changed current: %s", diff)
				}

				coverage.Hit("retired process start ignored")

				current = apply(
					scan.Add(2*time.Second),
					report(scan.Add(2*time.Second), &first, new(NativeLifecycleEnd), 101),
				)
				if diff := cmp.Diff(prior, current); diff != "" {
					t.Fatalf("old process end changed current: %s", diff)
				}

				coverage.Hit("old process end ignored")
			case "equivalent sequence replay":
				before := cloneRegistrySnapshot(snapshot{Sessions: state.Sessions}).Sessions

				after, changes, err := NewReducer(fixtureRules{}).
					Apply(state, []Observation{initial}, scan)
				if err != nil {
					t.Fatal(err)
				}

				if diff := cmp.Diff(before, after.Sessions); diff != "" {
					t.Fatalf("equivalent sequence replay changed sessions: %s", diff)
				}

				if len(changes) != 0 {
					t.Fatalf("equivalent sequence replay emitted changes: %v", changes)
				}

				if err := checkInvariantSessions(mapSessions(after.Sessions)); err != nil {
					t.Fatal(err)
				}
			case "sequence content conflict", "stale terminal sequence conflict":
				if situation == "stale terminal sequence conflict" {
					apply(scan, sighting(scan, first, true))
				}

				before := State{
					Sessions:  cloneRegistrySnapshot(snapshot{Sessions: state.Sessions}).Sessions,
					UpdatedAt: state.UpdatedAt,
				}
				unrelated := report(base.Add(time.Second), nil, nil, 1)
				unrelated.Subject.SessionID = "unrelated"
				conflict := report(base, &first, nil, 90)

				conflict.Report().Activity = new(ActivityIdle)
				if situation == "stale terminal sequence conflict" {
					conflict = report(base.Add(time.Second), &first, new(NativeLifecycleEnd), 90)
				}

				_, acceptedChanges, err := NewReducer(fixtureRules{}).
					Apply(state, []Observation{unrelated}, scan)
				if err != nil || len(acceptedChanges) == 0 {
					t.Fatalf(
						"atomicity setup did not produce an accepted change: %v, %v",
						acceptedChanges,
						err,
					)
				}

				after, changes, err := NewReducer(fixtureRules{}).
					Apply(state, []Observation{unrelated, conflict}, scan)
				if !errors.Is(err, ErrObservationConflict) {
					t.Fatalf(
						"equal sequence content conflict = %v, want ErrObservationConflict",
						err,
					)
				}

				if diff := cmp.Diff(before, after); diff != "" {
					t.Fatalf("sequence content conflict changed returned state: %s", diff)
				}

				if diff := cmp.Diff(before, state); diff != "" {
					t.Fatalf("sequence content conflict changed input state: %s", diff)
				}

				if len(changes) != 0 {
					t.Fatalf("sequence content conflict emitted changes: %v", changes)
				}

				if err := checkInvariantSessions(mapSessions(after.Sessions)); err != nil {
					t.Fatal(err)
				}
			case "future report process exit":
				current = apply(
					base.Add(time.Second),
					report(base.Add(4*time.Minute), &first, nil, 91),
				)
				if current.Observations.Native.ObservedAt.After(base.Add(time.Second)) {
					t.Fatal("future report not clamped")
				}

				prior := cloneSessionValue(current)

				current = apply(
					base.Add(2*time.Second),
					sighting(base.Add(-time.Second), first, false),
				)
				if diff := cmp.Diff(prior, current); diff != "" {
					t.Fatalf("old exit changed session: %s", diff)
				}

				current = apply(
					base.Add(2*time.Second),
					sighting(base.Add(2*time.Second), first, false),
				)
				if current.Incarnation.State != IncarnationExited || current.Activity() != nil {
					t.Fatal("fresh exit did not terminate")
				}
			case "identity tombstone merge":
				apply(
					scan,
					Observation{
						Harness:  HarnessPi,
						At:       scan,
						Evidence: &Placement{Process: second},
					},
				)

				current = apply(
					scan.Add(time.Second),
					report(scan.Add(time.Second), &second, nil, 1),
				)
				if current.Presence() == PresenceGone ||
					current.Incarnation.State == IncarnationEnded ||
					*current.Observations.Native.Reporter.Sequence != 1 {
					t.Fatal("identity tombstone contaminated process incarnation")
				}

				coverage.Hit("sequence baseline")
			case "terminal time monotonic":
				apply(scan, sighting(scan, first, false))

				current = apply(scan, sighting(base.Add(time.Second), first, false))
				if !current.Incarnation.TransitionAt.Equal(scan) {
					t.Fatal("terminal time regressed")
				}

				current = apply(
					scan,
					report(base.Add(2*time.Second), &first, new(NativeLifecycleStart), 91),
				)
				if current.Presence() != PresenceGone {
					t.Fatal("start before own exit revived session")
				}
			case "ordering drop atomic matching":
				stale := report(base.Add(-time.Second), &first, nil, 1)
				stale.Subject.SessionID = "conflicting"
				apply(scan, stale)

				if len(state.Sessions) != 1 {
					t.Fatal("dropped observation changed matching")
				}
			}

			coverage.Hit(situation)
			coverage.Hit("one nonterminal current")
		})
	}

	if missing := coverage.Missing(
		"delayed end",
		"delayed absence",
		"resume before scan",
		"future report process exit",
		"sequence baseline",
		"identity tombstone merge",
		"terminal Gone no activity",
		"stale own-process terminal no-op",
		"equal own-process terminal",
		"fresh own-process terminal",
		"one nonterminal current",
		"retired process start ignored",
		"ordering drop atomic matching",
		"terminal time monotonic",
		"replaced session end isolated",
		"old process end ignored",
		"equivalent sequence replay",
		"sequence content conflict",
		"stale terminal sequence conflict",
	); len(
		missing,
	) > 0 {
		t.Fatalf("uncovered incarnation situations: %v", missing)
	}
}
