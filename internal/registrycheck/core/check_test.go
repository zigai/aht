package core

import (
	"errors"
	"testing"
	"time"
)

func TestGoneSessionCannotHaveActivity(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	err := Sessions(
		[]Session{
			{
				ID:          "gone",
				Harness:     "fixture",
				CreatedAt:   at,
				UpdatedAt:   at,
				HasLiveness: true,
				Presence:    "gone",
				Activity:    "running",
				ValidState:  true,
			},
		},
	)
	if !errors.Is(err, errGoneActivity) {
		t.Fatalf("gone activity error = %v", err)
	}
}

func TestIncarnationInvariants(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	valid := Session{
		ID:                 "current",
		SessionID:          "native",
		Harness:            "fixture",
		CreatedAt:          at,
		UpdatedAt:          at,
		HasLiveness:        true,
		ValidState:         true,
		Presence:           "live",
		Activity:           "running",
		Incarnation:        "live",
		Process:            "1:a",
		IncarnationProcess: "1:a",
		EvidenceAt:         at,
	}
	coverage := NewCoverage()

	for _, situation := range []string{
		"terminal Gone no activity",
		"one nonterminal current",
		"process identity consistency",
	} {
		sessions := []Session{valid}

		switch situation {
		case "terminal Gone no activity":
			sessions[0].Incarnation = "ended"
		case "one nonterminal current":
			duplicate := valid
			duplicate.ID = "duplicate"
			sessions = append(sessions, duplicate)
		case "process identity consistency":
			sessions[0].IncarnationProcess = "1:b"
		}

		if err := Sessions(sessions); err == nil {
			t.Fatalf("missing %s invariant", situation)
		}

		coverage.Hit(situation)
	}

	if missing := coverage.Missing(
		"terminal Gone no activity",
		"one nonterminal current",
		"process identity consistency",
	); len(
		missing,
	) > 0 {
		t.Fatalf("missing coverage: %v", missing)
	}
}

func TestIncarnationCrossStepInvariants(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	before := Session{
		ID:          "current",
		Harness:     "fixture",
		SessionID:   "native",
		SessionPath: "/native/current",
		Process:     "1:a",
		Incarnation: "live",
		Presence:    "live",
		Activity:    "running",
		EvidenceAt:  at,
		DecisionAt:  at,
	}
	for _, situation := range []string{
		"delayed end",
		"future report process exit",
		"old process end",
		"exit scan evidence",
		"activity ordering",
		"other native identity end",
		"other harness end",
		"other native path end",
		"other native identity decision changed",
	} {
		after := before
		kind, process, ends, present := "report", "1:a", true, false
		harness, sessionID, sessionPath := before.Harness, before.SessionID, before.SessionPath
		eventAt := at

		switch situation {
		case "delayed end":
			eventAt = at.Add(-time.Second)
			after.NativeEnded = true
		case "future report process exit":
			kind = "sighting"
		case "old process end":
			process = "1:b"
			after.Process = process
		case "exit scan evidence":
			kind = "sighting"
			after.Presence = "gone"
			after.Activity = ""
			after.EvidenceAt = at.Add(time.Second)
		case "activity ordering":
			ends = false
			after.DecisionAt = at.Add(-time.Second)
		case "other native identity end":
			sessionID = "other"
			after.Presence, after.Activity, after.Incarnation = "gone", "", "ended"
		case "other native identity decision changed":
			sessionID = "other"
			after.DecisionAt = at.Add(time.Second)
		case "other harness end":
			harness = "other"
			after.Presence, after.Activity, after.Incarnation = "gone", "", "ended"
		case "other native path end":
			sessionID, sessionPath = "", "/native/other"
			after.Presence, after.Activity, after.Incarnation = "gone", "", "ended"
		}

		if err := Transition(before, after, Event{
			Process: process, Harness: harness,
			SessionID: sessionID, SessionPath: sessionPath, At: eventAt,
			NativeEnd: kind == "report" && ends, Absent: kind == "sighting" && !present,
		}); err == nil {
			t.Errorf("missing %s invariant", situation)
		}
	}

	coverage := NewCoverage()

	for _, kind := range []string{"report", "sighting"} {
		for _, boundary := range []string{"stale", "equal", "fresh"} {
			event := Event{
				Process: before.Process, Harness: before.Harness, SessionID: before.SessionID,
				At: at, NativeEnd: kind == "report", Absent: kind == "sighting",
			}
			after := before

			switch boundary {
			case "stale":
				event.At = at.Add(-time.Nanosecond)
			case "fresh":
				event.At = at.Add(time.Nanosecond)
			}

			if boundary != "stale" {
				after.Presence, after.Activity, after.Incarnation = "gone", "", "exited"
				if kind == "report" {
					after.Incarnation, after.NativeEnded = "ended", true
				}
			}

			if err := Transition(before, after, event); err != nil {
				t.Fatalf("valid %s %s: %v", boundary, kind, err)
			}

			if boundary == "stale" {
				after.UpdatedAt = at.Add(time.Second)
			} else {
				after = before
			}

			if err := Transition(before, after, event); err == nil {
				t.Fatalf("missing %s %s terminal ordering invariant", boundary, kind)
			}

			coverage.Hit(boundary + " " + kind)
		}
	}

	if missing := coverage.Missing(
		"stale report", "equal report", "fresh report",
		"stale sighting", "equal sighting", "fresh sighting",
	); len(missing) > 0 {
		t.Fatalf("missing terminal ordering coverage: %v", missing)
	}
}
