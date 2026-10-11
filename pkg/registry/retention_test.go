package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func retentionProcess(pid int) ProcessIdentity {
	return ProcessIdentity{PID: pid, PPID: 1, ProcessGroupID: pid, StartIdentity: "boot:" + strconv.Itoa(pid), Executable: "/usr/bin/codex"}
}

func storedSession(id string, pid int, sessionID string, createdAt time.Time) Session {
	session := newSession(id, HarnessCodex, createdAt)
	process := retentionProcess(pid)
	session.Process = &process
	if sessionID != "" {
		session.IdentityState = IdentityIdentified
		session.SessionID = sessionID
	}
	session.setPresence(PresenceLive)
	return session
}

func goneStoredSession(id string, pid int, sessionID string, goneAt time.Time) Session {
	session := storedSession(id, pid, sessionID, goneAt.Add(-time.Minute))
	session.Liveness = Gone{At: goneAt, Reason: "process_gone", Decision: nil}
	session.PresenceChangedAt = goneAt
	session.UpdatedAt = goneAt
	return session
}

// writeLegacySnapshot writes sessions the way earlier releases did: indented
// JSON that retained every gone session.
func writeLegacySnapshot(t *testing.T, path string, sessions ...Session) {
	t.Helper()
	snap := newSnapshot()
	for _, session := range sessions {
		snap.Sessions[session.ID] = session
		snap.UpdatedAt = maxTime(snap.UpdatedAt, session.UpdatedAt)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sessionIDs(sessions []Session) map[string]bool {
	ids := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		ids[session.ID] = true
	}
	return ids
}

type fileMark struct {
	inode   uint64
	modTime time.Time
	size    int64
}

func markFile(t *testing.T, path string) fileMark {
	t.Helper()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileMark{}
	}
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("file inode unavailable on this platform")
	}
	return fileMark{inode: stat.Ino, modTime: info.ModTime(), size: info.Size()}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(failure)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMemoryStoreStartupPrunesToLiveSetAndRecentIdentifiedTombstones(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Now().UTC()
	sessions := make([]Session, 0, 58)
	for index := range 40 {
		// Process-only gone records, like short-lived command-name matches.
		sessions = append(sessions, goneStoredSession("provisional-"+strconv.Itoa(index), 1000+index, "", now.Add(-time.Duration(index)*time.Second)))
	}
	for index := range 15 {
		sessions = append(sessions, goneStoredSession("expired-"+strconv.Itoa(index), 2000+index, "native-expired-"+strconv.Itoa(index), now.Add(-time.Hour)))
	}
	sessions = append(sessions,
		goneStoredSession("recent", 3000, "native-recent", now.Add(-time.Minute)),
		storedSession("live-identified", 3001, "native-live", now.Add(-time.Hour)),
		storedSession("live-provisional", 3002, "", now.Add(-time.Hour)),
	)
	writeLegacySnapshot(t, path, sessions...)

	store, err := OpenMemoryStoreWithOptions(path, fixtureRules{}, MemoryStoreOptions{TombstoneTTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	want := map[string]bool{"recent": true, "live-identified": true, "live-provisional": true}
	listed, err := store.List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(listed); len(got) != len(want) || !got["recent"] || !got["live-identified"] || !got["live-provisional"] {
		t.Fatalf("memory sessions after startup = %v, want %v", got, want)
	}
	persisted, err := NewJournal(path, fixtureRules{}).List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(persisted); len(got) != len(want) || !got["recent"] {
		t.Fatalf("persisted sessions after startup = %v, want %v", got, want)
	}
}

func TestMemoryStoreContinuouslyExpiresIdentifiedTombstonesAfterTTL(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	base := time.Now().UTC()
	writeLegacySnapshot(t, path,
		goneStoredSession("tombstone", 4000, "native-tombstone", base),
		storedSession("live", 4001, "native-live", base),
	)
	store, err := OpenMemoryStoreWithOptions(path, fixtureRules{}, MemoryStoreOptions{TombstoneTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := base.Add(30 * time.Second)
	store.setNowForTest(func() time.Time { return clock })
	if _, err := store.Get(t.Context(), "tombstone"); err != nil {
		t.Fatalf("tombstone inside TTL = %v, want retained", err)
	}
	before, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	clock = base.Add(2 * time.Minute)
	store.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- store.RunPersistence(ctx, 0, 0) }()
	waitFor(t, 5*time.Second, func() bool {
		_, err := store.Get(t.Context(), "tombstone")
		return errors.Is(err, ErrSessionNotFound)
	}, "tombstone was not expired by the persistence loop")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision <= before.Revision || len(after.Sessions) != 1 || after.Sessions[0].ID != "live" {
		t.Fatalf("state after expiry = revision %d sessions %d, want new revision with only live", after.Revision, len(after.Sessions))
	}
	if _, err := NewJournal(path, fixtureRules{}).Get(t.Context(), "tombstone"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired tombstone persisted: %v", err)
	}
}

func TestProcessOnlySessionIsRemovedWhenItGoesGone(t *testing.T) {
	t.Parallel()
	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Now().UTC()
	store.setNowForTest(func() time.Time { return base })
	process := retentionProcess(5000)
	session, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: base, Evidence: &Sighting{Process: process, Present: true}})
	if err != nil {
		t.Fatal(err)
	}
	if session.IdentityState != IdentityProvisional {
		t.Fatalf("identity = %q, want provisional", session.IdentityState)
	}
	live, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}

	gone, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: base.Add(time.Second), Evidence: &Sighting{Process: process, Present: false}})
	if err != nil {
		t.Fatal(err)
	}
	if gone.Presence() != PresenceGone {
		t.Fatalf("returned session presence = %s, want gone", gone.Presence())
	}
	changed, err := store.WaitForRevision(t.Context(), live.Revision, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Sessions) != 0 {
		t.Fatalf("sessions after process-only gone = %#v, want removed", changed.Sessions)
	}
	if _, err := store.Get(t.Context(), session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Get removed session = %v, want not found", err)
	}
}

func TestLateNativeReportRejectedWithinTombstoneTTL(t *testing.T) {
	t.Parallel()
	store, err := OpenMemoryStoreWithOptions(filepath.Join(t.TempDir(), "state.json"), fixtureRules{}, MemoryStoreOptions{TombstoneTTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Now().UTC()
	clock := base
	store.setNowForTest(func() time.Time { return clock })
	process := retentionProcess(6000)
	running, idle := ActivityRunning, ActivityIdle
	identity := ObservationIdentity{SessionID: "native-late"}
	session, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: base, Subject: identity, Evidence: &Report{Event: "agent_start", Activity: &running, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: base.Add(time.Second), Evidence: &Sighting{Process: process, Present: false}}); err != nil {
		t.Fatal(err)
	}

	clock = base.Add(5 * time.Minute)
	late, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: base.Add(2 * time.Second), Subject: identity, Evidence: &Report{Event: "agent_end", Activity: &idle, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}
	if late.ID != session.ID || late.Presence() != PresenceGone {
		t.Fatalf("late report = %s %s, want retained tombstone %s gone", late.ID, late.Presence(), session.ID)
	}
	listed, err := store.List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Presence() != PresenceGone {
		t.Fatalf("sessions after late report = %#v, want one gone tombstone", listed)
	}
}

func runPersistenceInBackground(t *testing.T, store *MemoryStore) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- store.RunPersistence(ctx, 0, 0) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}

func assertNoPersistence(t *testing.T, store *MemoryStore, path string, snapshot, journal fileMark, revision uint64) {
	t.Helper()
	if after := markFile(t, path); after != snapshot {
		t.Fatalf("heartbeats rewrote the snapshot: before %+v after %+v", snapshot, after)
	}
	if after := markFile(t, path+".journal.jsonl"); after != journal || after.size != 0 {
		t.Fatalf("heartbeats appended the journal: before %+v after %+v", journal, after)
	}
	state, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != revision {
		t.Fatalf("heartbeats advanced revision %d -> %d", revision, state.Revision)
	}
}

func TestHeartbeatObservationsDoNotJournalOrRewriteSnapshot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runPersistenceInBackground(t, store)

	process := retentionProcess(7000)
	location := Location{Kind: MultiplexerTmux, ServerID: "srv", SessionName: "work", PaneID: "%1", PanePID: 7000}
	cycle := func(at time.Time) []Observation {
		return []Observation{
			{Harness: HarnessCodex, At: at, Evidence: &Sighting{Process: process, Present: true}},
			{Harness: HarnessCodex, At: at, Evidence: &Placement{Process: process, Location: location}},
		}
	}
	at := time.Now().UTC()
	if _, err := store.ObserveBatch(t.Context(), cycle(at)); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshotBefore := markFile(t, path)
	journalBefore := markFile(t, path+".journal.jsonl")
	stateBefore, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}

	// Heartbeats spaced past the settle and maximum persistence delays.
	for range 12 {
		at = at.Add(300 * time.Millisecond)
		if _, err := store.ObserveBatch(t.Context(), cycle(at)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	time.Sleep(2 * defaultPersistenceMaxDelay)

	assertNoPersistence(t, store, path, snapshotBefore, journalBefore, stateBefore.Revision)

	// A visible change is journaled at once and checkpointed promptly.
	running := ActivityRunning
	if _, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: at.Add(time.Second), Subject: ObservationIdentity{SessionID: "native-visible"}, Evidence: &Report{Event: "agent_start", Activity: &running, Process: &process}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return markFile(t, path) != snapshotBefore }, "visible change was not persisted promptly")
	assertPersistedRunning(t, path, "native-visible")
}

func assertPersistedRunning(t *testing.T, path, sessionID string) {
	t.Helper()
	persisted, err := NewJournal(path, fixtureRules{}).List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].SessionID != sessionID || persisted[0].Activity() == nil || *persisted[0].Activity() != ActivityRunning {
		t.Fatalf("persisted sessions = %#v, want running %s", persisted, sessionID)
	}
}

func TestVisibleChangeSurvivesOwnerCrashThroughJournal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	process := retentionProcess(8000)
	running := ActivityRunning
	session, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: time.Now().UTC(), Subject: ObservationIdentity{SessionID: "crash"}, Evidence: &Report{Event: "agent_start", Activity: &running, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + ".journal.jsonl"); err != nil || info.Size() == 0 {
		t.Fatalf("visible change was not journaled: %v %v", info, err)
	}
	// Simulate a crash: release ownership without the final flush.
	store.mu.Lock()
	if err := closeStoreLock(store.owner, nil); err != nil {
		t.Fatal(err)
	}
	store.owner = nil
	store.mu.Unlock()

	reopened, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restored, err := reopened.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("journaled session lost across owner restart: %v", err)
	}
	if restored.Activity() == nil || *restored.Activity() != ActivityRunning {
		t.Fatalf("restored session = %#v, want running", restored)
	}
}

func TestLegacyIndentedSnapshotsLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Now().UTC()
	writeLegacySnapshot(t, path, storedSession("legacy", 9000, "native-legacy", now))

	store, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil || header.SchemaVersion != storeSchemaVersion {
		t.Fatalf("snapshot schema = %d, %v; want %d", header.SchemaVersion, err, storeSchemaVersion)
	}
	loaded, err := NewJournal(path, fixtureRules{}).Get(t.Context(), "legacy")
	if err != nil || loaded.SessionID != "native-legacy" {
		t.Fatalf("legacy snapshot reload = %#v, %v", loaded, err)
	}
}

// endedProcessOnlySession records a process-only session for process and then
// its exit, which removes the session and records the process as ended.
func endedProcessOnlySession(t *testing.T, store Store, process ProcessIdentity, at time.Time) {
	t.Helper()
	for index, present := range []bool{true, false} {
		sighting := Observation{Harness: HarnessCodex, At: at.Add(time.Duration(index) * time.Second), Evidence: &Sighting{Process: process, Present: present}}
		if _, err := store.Observe(t.Context(), sighting); err != nil {
			t.Fatal(err)
		}
	}
}

func lateReport(process ProcessIdentity, sessionID string, at time.Time) Observation {
	running := ActivityRunning
	return Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Report{Event: "agent_start", Activity: &running, Process: &process}}
}

func assertNoSessions(t *testing.T, store Store) {
	t.Helper()
	listed, err := store.List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("sessions = %#v, want none", listed)
	}
}

func TestLateReportFromEndedProcessIsDroppedAcrossRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	process := retentionProcess(7000)
	endedProcessOnlySession(t, store, process, base)

	if _, err := store.Observe(t.Context(), lateReport(process, "native-late", base.Add(3*time.Second))); !errors.Is(err, ErrProcessEnded) {
		t.Fatalf("late report error = %v, want ErrProcessEnded", err)
	}
	assertNoSessions(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.Observe(t.Context(), lateReport(process, "native-late", base.Add(4*time.Second))); !errors.Is(err, ErrProcessEnded) {
		t.Fatalf("late report after restart error = %v, want ErrProcessEnded", err)
	}
	assertNoSessions(t, reopened)

	reused := process
	reused.StartIdentity = "boot:reused"
	session, err := reopened.Observe(t.Context(), lateReport(reused, "native-reused-pid", base.Add(5*time.Second)))
	if err != nil {
		t.Fatalf("report from a reused pid = %v, want accepted", err)
	}
	if session.SessionID != "native-reused-pid" {
		t.Fatalf("reused pid session = %q, want native-reused-pid", session.SessionID)
	}
}

func TestEndedProcessesExpireWithTombstoneTTL(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := OpenMemoryStoreWithOptions(path, fixtureRules{}, MemoryStoreOptions{TombstoneTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Now().UTC()
	clock := base
	store.setNowForTest(func() time.Time { return clock })
	process := retentionProcess(7100)
	endedProcessOnlySession(t, store, process, base)
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(t.Context(), lateReport(process, "native-late", base.Add(3*time.Second))); !errors.Is(err, ErrProcessEnded) {
		t.Fatalf("late report before TTL = %v, want ErrProcessEnded", err)
	}

	clock = base.Add(2 * time.Minute)
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(t.Context(), lateReport(process, "native-late", clock)); err != nil {
		t.Fatalf("late report after TTL = %v, want accepted", err)
	}
}

func TestFallbackJournalDropsLateReportAndGCForgetsEndedProcess(t *testing.T) {
	t.Parallel()
	journal := NewJournal(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	base := time.Now().UTC().Add(-time.Minute)
	process := retentionProcess(7200)
	endedProcessOnlySession(t, journal, process, base)

	if _, err := journal.Observe(t.Context(), lateReport(process, "native-late", base.Add(3*time.Second))); !errors.Is(err, ErrProcessEnded) {
		t.Fatalf("late report error = %v, want ErrProcessEnded", err)
	}
	assertNoSessions(t, journal)

	if _, err := journal.GC(t.Context(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Observe(t.Context(), lateReport(process, "native-late", base.Add(4*time.Second))); !errors.Is(err, ErrProcessEnded) {
		t.Fatalf("late report after unexpired GC = %v, want ErrProcessEnded", err)
	}

	if _, err := journal.GC(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Observe(t.Context(), lateReport(process, "native-late", base.Add(5*time.Second))); err != nil {
		t.Fatalf("report after GC forgot the process = %v, want accepted", err)
	}
}
