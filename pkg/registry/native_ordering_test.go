package registry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReportAndProcessObservationKeepLivePresence(t *testing.T) {
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	base := time.Now().UTC().Add(-time.Minute)
	process := ProcessIdentity{
		PID:           4242,
		PPID:          1,
		StartIdentity: "boot:4242",
		Executable:    "/usr/bin/node",
	}
	live, running := PresenceLive, ActivityRunning
	subject := ObservationIdentity{SessionID: "native-session"}
	// The observation clock and the report clock can disagree by a few
	// milliseconds, and both orders have to keep the session live.
	sightingAt := base.Add(time.Second)
	reportAt := base.Add(time.Second - time.Millisecond)

	report := Observation{
		Harness: harnessSecondary,
		At:      reportAt,
		Subject: subject,
		Evidence: &Report{
			Event:    "turn_end",
			Activity: &running,
			Claim:    &live,
			Process:  &process,
		},
	}
	sighting := Observation{
		Harness:  harnessSecondary,
		At:       sightingAt,
		Subject:  subject,
		Evidence: &Sighting{Process: process, Present: true},
	}

	for name, batch := range map[string][]Observation{
		"report first":   {report, sighting},
		"sighting first": {sighting, report},
	} {
		t.Run(name, func(t *testing.T) {
			sessions, err := store.ObserveBatch(t.Context(), batch)
			if err != nil {
				t.Fatal(err)
			}

			if len(sessions) == 0 {
				t.Fatal("no session")
			}

			session := sessions[len(sessions)-1]
			if session.Presence() != PresenceLive {
				t.Fatalf("presence = %q, want live", session.Presence())
			}

			if session.Observations.Native == nil {
				t.Fatal("native report was dropped")
			}

			if session.Activity() == nil || *session.Activity() != ActivityRunning {
				t.Fatalf("activity = %v, want running", session.Activity())
			}
		})
	}
}
