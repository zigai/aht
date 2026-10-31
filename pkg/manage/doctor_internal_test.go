package manage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDoctorHealthFollowsTrackerService(t *testing.T) {
	fresh := time.Now().UTC().Format(time.RFC3339Nano)

	tests := []struct {
		name    string
		service DoctorStatus
		health  string
		want    DoctorStatus
	}{
		{
			name:    "service not running",
			service: DoctorStatusWarn,
			health:  `{"interval":300000000,"last_success_at":"` + fresh + `"}`,
			want:    DoctorStatusSkip,
		},
		{name: "service failed", service: DoctorStatusFail, health: "", want: DoctorStatusSkip},
		{
			name:    "healthy",
			service: DoctorStatusOK,
			health:  `{"interval":300000000,"last_success_at":"` + fresh + `"}`,
			want:    DoctorStatusOK,
		},
		{name: "missing", service: DoctorStatusOK, health: "", want: DoctorStatusWarn},
		{
			name:    "never reconciled",
			service: DoctorStatusOK,
			health:  `{"interval":300000000}`,
			want:    DoctorStatusWarn,
		},
		{
			name:    "stale",
			service: DoctorStatusOK,
			health:  `{"interval":300000000,"last_success_at":"2020-01-01T00:00:00Z"}`,
			want:    DoctorStatusWarn,
		},
		{
			name:    "degraded",
			service: DoctorStatusOK,
			health:  `{"interval":300000000,"degraded":true,"last_enumeration_error":"permission denied"}`,
			want:    DoctorStatusWarn,
		},
		{name: "corrupt", service: DoctorStatusOK, health: "nope{", want: DoctorStatusFail},
		{
			name:    "service unknown",
			service: DoctorStatusSkip,
			health:  `{"interval":300000000,"last_success_at":"2020-01-01T00:00:00Z"}`,
			want:    DoctorStatusWarn,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := New(Config{StorePath: filepath.Join(t.TempDir(), "state.json")})
			if test.health != "" {
				if err := os.WriteFile(
					manager.HealthPath(),
					[]byte(test.health),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}

			serviceCheck := newDoctorCheck(doctorGroupTracker, "service")
			serviceCheck.Status = test.service

			check := manager.checkHealth(0, serviceCheck)

			if check.Status != test.want {
				t.Fatalf("tracker.health = %+v, want %s", check, test.want)
			}

			if test.want == DoctorStatusSkip && check.Needs != doctorCheckService {
				t.Fatalf("tracker.health needs %q, want %q", check.Needs, doctorCheckService)
			}

			if (check.Status == DoctorStatusWarn || check.Status == DoctorStatusFail) &&
				check.Fix == nil {
				t.Fatalf("tracker.health is %s without a fix", check.Status)
			}
		})
	}
}
