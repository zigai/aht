package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func FuzzJournalRecovery(f *testing.F) {
	validSnapshot, err := json.Marshal(newSnapshot())
	if err != nil {
		f.Fatal(err)
	}

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	entry := journalEntry{
		Sequence:   1,
		ReceivedAt: base,
		Observations: []Observation{
			{
				Harness:  harnessSecondary,
				At:       base,
				Subject:  ObservationIdentity{SessionID: "fuzz"},
				Evidence: &Report{Activity: new(ActivityRunning)},
			},
		},
	}

	line, err := json.Marshal(entry)
	if err != nil {
		f.Fatal(err)
	}

	line = append(line, '\n')

	f.Add([]byte(nil), []byte(nil))
	f.Add(validSnapshot, line)
	f.Add(validSnapshot, append(bytes.Clone(line), []byte(`{"sequence":`)...))
	f.Add(validSnapshot, append(bytes.Clone(line), line...))
	f.Add(validSnapshot, append([]byte(`{"sequence":2}`+"\n"), line...))

	invalidSnapshot := newSnapshot()
	invalidSnapshot.Sessions["bad"] = Session{}

	invalidBytes, err := json.Marshal(invalidSnapshot)
	if err != nil {
		f.Fatal(err)
	}

	f.Add(invalidBytes, []byte("garbage\n"))
	f.Add([]byte("{"), []byte("{"))
	f.Fuzz(fuzzJournalCase)
}

func fuzzJournalCase(t *testing.T, snapshotBytes []byte, journalBytes []byte) {
	if len(snapshotBytes) > 64<<10 || len(journalBytes) > 64<<10 {
		t.Skip()
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if len(snapshotBytes) > 0 {
		if err := os.WriteFile(path, snapshotBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	journalPath := journalPath(path)
	if len(journalBytes) > 0 {
		if err := os.WriteFile(journalPath, journalBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := recoverFuzzJournal(path)
	if err == nil {
		if err := validateSnapshot(snap); err != nil {
			t.Fatalf("recovered invalid state: %v", err)
		}
	}

	if len(journalBytes) > 0 && journalBytes[len(journalBytes)-1] != '\n' {
		checkAppendAfterTornTail(t, path, journalBytes, snap.JournalSequence)
	}
}

func recoverFuzzJournal(path string) (snapshot, error) {
	return NewJournal(path, fixtureRules{}).load(context.Background())
}

func checkAppendAfterTornTail(t *testing.T, path string, journalBytes []byte, sequence uint64) {
	t.Helper()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := appendJournal(
		path,
		journalEntry{Sequence: sequence + 1, ReceivedAt: base},
	); err != nil {
		t.Fatalf("append after torn tail: %v", err)
	}

	data, err := os.ReadFile(journalPath(path))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("append left incomplete tail")
	}

	prefix := journalBytes[:bytes.LastIndexByte(journalBytes, '\n')+1]
	if !bytes.HasPrefix(data, prefix) {
		t.Fatal("append altered completed entries")
	}
}
