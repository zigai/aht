package registry

import (
	"fmt"
	rand "math/rand/v2"
	"path/filepath"
	"testing"
	"time"
)

//nolint:gocognit,cyclop // The fuzz driver checks every decoded operation before advancing.
func FuzzStoreParity(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 1, 5, 6, 7, 8, 4, 0, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 0, 0, 2, 1, 0, 0, 0, 3, 0, 0, 0, 0})
	f.Add([]byte{1, 9, 9, 9, 9, 5, 2, 0, 0, 0, 0, 8, 7, 6, 5})
	f.Add([]byte{4, 0, 0, 0, 0, 0, 3, 4, 5, 6, 2, 0, 0, 0, 0})
	f.Add([]byte{6, 1, 2, 0, 0, 7, 3, 0, 0, 0, 8, 4, 0, 0, 0, 9, 5, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		const width, maxOps = 5, 40
		base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
		journalPath := filepath.Join(t.TempDir(), "journal.json")
		journal := NewJournal(journalPath, fixtureRules{})
		memory, err := OpenMemoryStore(filepath.Join(t.TempDir(), "memory.json"), fixtureRules{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := memory.Close(); err != nil {
				t.Error(err)
			}
		})
		clock := base.Add(time.Hour)
		journal.setNowForTest(func() time.Time { return clock })
		memory.setNowForTest(func() time.Time { return clock })
		sequences := make(map[string]uint64)
		for step, offset := 0, 0; step < maxOps && (step == 0 || offset < len(data)); step, offset = step+1, offset+width {
			var word [width]byte
			copy(word[:], data[offset:min(offset+width, len(data))])
			label := fmt.Sprintf("input %x step %d", data, step)
			switch word[0] % 10 {
			case 0:
				generator := rand.New(rand.NewPCG(uint64(word[1])<<24|uint64(word[2])<<16|uint64(word[3])<<8|uint64(word[4]), uint64(step))) //nolint:gosec // Reproducible fuzz input.
				observation := generatedObservation(generator, sequences, base, step)
				fileSession, fileErr := journal.Observe(t.Context(), observation)
				memSession, memErr := memory.Observe(t.Context(), observation)
				if !sameStoreError(fileErr, memErr) {
					t.Fatalf("%s observe errors: file %v, memory %v", label, fileErr, memErr)
				}
				if fileErr == nil {
					assertEquivalent(t, label+" observe", fileSession, memSession)
				}
			case 1:
				injectParityConflictBatch(t, journal, memory, base, step)
			case 2:
				before := mustListParity(t, journal)
				deleteAfter := time.Duration(int(word[1]%4)-1) * time.Hour
				fileResult, fileErr := journal.GC(t.Context(), deleteAfter)
				memResult, memErr := memory.GC(t.Context(), deleteAfter)
				if !sameStoreError(fileErr, memErr) {
					t.Fatalf("%s gc errors: file %v, memory %v", label, fileErr, memErr)
				}
				if fileErr == nil {
					assertEquivalent(t, label+" gc", fileResult, memResult)
					assertGCOnlyExpiredGone(t, before, mustListParity(t, journal), clock, deleteAfter)
				}
			case 3:
				fileResult, fileErr := journal.Reset(t.Context())
				memResult, memErr := memory.Reset(t.Context())
				if !sameStoreError(fileErr, memErr) {
					t.Fatalf("%s reset errors: file %v, memory %v", label, fileErr, memErr)
				}
				if fileErr == nil {
					assertEquivalent(t, label+" reset", fileResult, memResult)
				}
			case 4:
				journal = NewJournal(journalPath, fixtureRules{})
				journal.setNowForTest(func() time.Time { return clock })
			case 5:
				clock = clock.Add(time.Duration(word[1]) * time.Second)
			case 6:
				activity := ActivityWaiting
				batch := []Observation{
					{Harness: HarnessPi, At: base.Add(time.Duration(step) * time.Second), Subject: ObservationIdentity{SessionID: fmt.Sprintf("batch-%d-a", word[1])}, Evidence: &Report{Event: "waiting", Activity: &activity}},
					{Harness: HarnessPi, At: base.Add(time.Duration(step) * time.Second), Subject: ObservationIdentity{SessionID: fmt.Sprintf("batch-%d-b", word[2])}, Evidence: &Report{Event: "waiting", Activity: &activity}},
				}
				fileSaved, fileErr := journal.ObserveBatch(t.Context(), batch)
				memorySaved, memoryErr := memory.ObserveBatch(t.Context(), batch)
				if !sameStoreError(fileErr, memoryErr) {
					t.Fatalf("%s batch errors: file %v, memory %v", label, fileErr, memoryErr)
				}
				if fileErr == nil {
					assertEquivalent(t, label+" batch", fileSaved, memorySaved)
				}
			case 7:
				end := NativeLifecycleEnd
				observation := Observation{Harness: HarnessPi, At: base.Add(time.Duration(step) * time.Second), Subject: ObservationIdentity{SessionID: fmt.Sprintf("ended-%d", word[1])}, Evidence: &Report{Event: "end", Lifecycle: &end}}
				fileSession, fileErr := journal.Observe(t.Context(), observation)
				memorySession, memoryErr := memory.Observe(t.Context(), observation)
				if !sameStoreError(fileErr, memoryErr) {
					t.Fatalf("%s end errors: file %v, memory %v", label, fileErr, memoryErr)
				}
				if fileErr == nil {
					assertEquivalent(t, label+" end", fileSession, memorySession)
				}
			case 8:
				process := ProcessIdentity{PID: int(word[1]) + 1, StartIdentity: fmt.Sprintf("screen:%d", word[1]), Executable: "agent"}
				observation := Observation{Harness: HarnessPi, At: base.Add(time.Duration(step) * time.Second), Subject: ObservationIdentity{SessionID: fmt.Sprintf("screen-%d", word[1])}, Evidence: (*Reading)(&ScreenObservation{Activity: ActivityWaiting, Authority: AuthorityScreen, Reason: "fuzz", Process: process, ObservedAt: base.Add(time.Duration(step) * time.Second)})}
				fileSession, fileErr := journal.Observe(t.Context(), observation)
				memorySession, memoryErr := memory.Observe(t.Context(), observation)
				if !sameStoreError(fileErr, memoryErr) {
					t.Fatalf("%s screen errors: file %v, memory %v", label, fileErr, memoryErr)
				}
				if fileErr == nil {
					assertEquivalent(t, label+" screen", fileSession, memorySession)
				}
			case 9:
				exerciseReplacedSightingPair(t, journal, memory, base, uint64(word[1]), step, nil)
			}
			fileSessions := mustListParity(t, journal)
			memorySessions := mustListParity(t, memory)
			assertEquivalent(t, label+" sessions", fileSessions, memorySessions)
			if err := checkInvariantSessions(fileSessions); err != nil {
				t.Fatalf("%s invariants: %v", label, err)
			}
			assertExclusiveProcesses(t, fileSessions)
		}
	})
}
