package reporting

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPrepareReportValidationPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options Options
		want    error
	}{
		{
			"stdin conflict before harness",
			Options{RawStdin: true, RawDefaultsOnly: true},
			errConflictingReportStdin,
		},
		{
			"gone before sequence",
			Options{Harness: "codex", Presence: "gone", Activity: "running", Sequence: "bad"},
			errGonePresenceActivity,
		},
		{
			"missing identity before process identity",
			Options{Harness: "codex", Evidence: "process"},
			errMissingReportIdentity,
		},
		{
			"process pid before activity",
			Options{Harness: "codex", Evidence: "process", Activity: "running"},
			errProcessEvidenceIdentity,
		},
		{
			"process activity before sequence",
			Options{
				Harness:  "codex",
				Evidence: "process",
				PID:      42,
				Activity: "running",
				Sequence: "1",
			},
			errProcessEvidenceActivity,
		},
		{
			"process sequence before identity completeness",
			Options{
				Harness:   "codex",
				Evidence:  "process",
				PID:       42,
				SessionID: "s",
				Sequence:  "1",
			},
			errProcessEvidenceSequence,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Prepare(nil, test.options, Runtime{})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestPrepareReportDetailTimestampBoundaries(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	for _, detail := range []string{"permission", "clear", ""} {
		t.Run(detail, func(t *testing.T) {
			t.Parallel()

			prepared, err := Prepare(strings.NewReader(""), Options{
				Harness: "codex", SessionID: "s", Activity: "waiting", Detail: detail,
				DetailObservedAt: at.Format(time.RFC3339Nano),
			}, Runtime{DefaultObservedAt: at})
			if detail != "permission" {
				if !errors.Is(err, registry.ErrInvalidObservation) {
					t.Fatalf("error = %v", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got := prepared.Observation.Report().DetailObservedAt; got == nil || !got.Equal(at) {
				t.Fatalf("detail timestamp = %v, want %v", got, at)
			}
		})
	}
}
