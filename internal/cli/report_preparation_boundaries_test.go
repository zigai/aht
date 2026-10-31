package cli

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
		options reportOptions
		want    error
	}{
		{
			"stdin conflict before harness",
			reportOptions{rawStdin: true, rawDefaultsOnly: true},
			errConflictingReportStdin,
		},
		{
			"gone before sequence",
			reportOptions{harness: "codex", presence: "gone", activity: "running", sequence: "bad"},
			errGonePresenceActivity,
		},
		{
			"missing identity before process identity",
			reportOptions{harness: "codex", evidence: "process"},
			errMissingReportIdentity,
		},
		{
			"process pid before activity",
			reportOptions{harness: "codex", evidence: "process", activity: "running"},
			errProcessEvidenceIdentity,
		},
		{
			"process activity before sequence",
			reportOptions{
				harness:  "codex",
				evidence: "process",
				pid:      42,
				activity: "running",
				sequence: "1",
			},
			errProcessEvidenceActivity,
		},
		{
			"process sequence before identity completeness",
			reportOptions{
				harness:   "codex",
				evidence:  "process",
				pid:       42,
				sessionID: "s",
				sequence:  "1",
			},
			errProcessEvidenceSequence,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := prepareReport(nil, test.options, reportRuntimeContext{})
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

			prepared, err := prepareReport(strings.NewReader(""), reportOptions{
				harness: "codex", sessionID: "s", activity: "waiting", detail: detail,
				detailObservedAt: at.Format(time.RFC3339Nano),
			}, reportRuntimeContext{defaultObservedAt: at})
			if detail != "permission" {
				if !errors.Is(err, registry.ErrInvalidObservation) {
					t.Fatalf("error = %v", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got := prepared.observation.Report().DetailObservedAt; got == nil || !got.Equal(at) {
				t.Fatalf("detail timestamp = %v, want %v", got, at)
			}
		})
	}
}
