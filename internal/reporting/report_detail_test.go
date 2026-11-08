package reporting

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestReportDetailValidationAndNativePayload(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, activity, detail, payload string
		event                           string
		want                            registry.ActivityDetail
		invalid                         bool
	}{
		{"permission", "waiting", "permission", "", "", registry.ActivityDetailPermission, false},
		{"question", "waiting", "question", "", "", registry.ActivityDetailQuestion, false},
		{"limit", "failed", "usage_limit", "", "", registry.ActivityDetailUsageLimit, false},
		{"clear", "waiting", "clear", "", "", "", false},
		{"wrong state", "running", "permission", "", "", "", true},
		{"unknown type", "waiting", "plan", "", "", "", true},
		{"native permission", "waiting", "", `{"session_id":"s","cwd":"/work",` +
			`"hook_event_name":"PermissionRequest",` +
			`"permission_mode":"plan"}`, "PermissionRequest", registry.ActivityDetailPermission, false},
		{"terminal limit", "failed", "", `{"session_id":"s","cwd":"/work",` +
			`"hook_event_name":"StopFailure","error":"rate_limit",` +
			`"error_details":"SECRET"}`, "StopFailure", registry.ActivityDetailUsageLimit, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := Options{
				Harness:         "claude",
				SessionID:       "s",
				Activity:        test.activity,
				Detail:          test.detail,
				Event:           test.event,
				RawDefaultsOnly: test.payload != "",
			}

			result, err := Prepare(
				strings.NewReader(test.payload),
				options,
				Runtime{DefaultObservedAt: time.Now().UTC()},
			)
			if test.invalid {
				if !errors.Is(err, registry.ErrInvalidObservation) {
					t.Fatalf("invalid detail = %v", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if report := result.Observation.Report(); report.Detail == nil ||
				*report.Detail != test.want {
				t.Fatalf("detail = %+v", report.Detail)
			}

			if report := result.Observation.Report(); len(report.Payload) != 0 {
				t.Fatal("defaults-only report retained payload")
			}
		})
	}
}

func TestReportPreservesProducerDetailTime(t *testing.T) {
	t.Parallel()

	at := time.Now().UTC()
	original := at.Add(-time.Minute)

	result, err := Prepare(
		strings.NewReader(""),
		Options{
			Harness:          "claude",
			SessionID:        "s",
			Activity:         "waiting",
			Detail:           "permission",
			DetailObservedAt: original.Format(time.RFC3339Nano),
		},
		Runtime{DefaultObservedAt: at},
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Observation.Report().DetailObservedAt == nil ||
		!result.Observation.Report().DetailObservedAt.Equal(original) {
		t.Fatal("producer detail time was replaced")
	}

	for _, stamp := range []string{"invalid", at.Add(time.Second).Format(time.RFC3339Nano)} {
		_, err := Prepare(
			strings.NewReader(""),
			Options{
				Harness:          "claude",
				SessionID:        "s",
				Activity:         "waiting",
				Detail:           "permission",
				DetailObservedAt: stamp,
			},
			Runtime{DefaultObservedAt: at},
		)
		if !errors.Is(err, registry.ErrInvalidObservation) {
			t.Fatalf("invalid detail timestamp = %v", err)
		}
	}
}
