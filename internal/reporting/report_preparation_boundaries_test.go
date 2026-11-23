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

func TestPrepareRejectsFlagsTheEvidenceIgnores(t *testing.T) {
	t.Parallel()

	native := Options{Harness: "codex", SessionID: "s", Presence: "live"}
	process := Options{
		Harness: "codex", SessionID: "s", Presence: "live", Evidence: "process",
		PID: 42, StartIdentity: "boot:42",
	}
	withNative := func(change func(*Options)) Options {
		base := native
		change(&base)

		return base
	}
	withProcess := func(change func(*Options)) Options {
		base := process
		change(&base)

		return base
	}

	tests := []struct {
		name    string
		options Options
		want    error
	}{
		{
			"native start identity",
			withNative(func(opts *Options) { opts.StartIdentity = "boot:1" }),
			errNativeProcessFlags,
		},
		{"native ppid", withNative(func(opts *Options) { opts.PPID = 1 }), errNativeProcessFlags},
		{
			"native process group",
			withNative(func(opts *Options) { opts.ProcessGroupID = 1 }),
			errNativeProcessFlags,
		},
		{
			"native executable",
			withNative(func(opts *Options) { opts.Executable = "/usr/bin/codex" }),
			errNativeProcessFlags,
		},
		{
			"native tty",
			withNative(func(opts *Options) { opts.TTY = "/dev/pts/1" }),
			errNativeProcessFlags,
		},
		{
			"process project root",
			withProcess(func(opts *Options) { opts.ProjectRoot = "/work" }),
			errProcessEvidenceListing,
		},
		{
			"process resume command",
			withProcess(func(opts *Options) { opts.ResumeCommand = []string{"codex", "resume"} }),
			errProcessEvidenceListing,
		},
		{
			"process cwd without start identity",
			withProcess(func(opts *Options) { opts.StartIdentity, opts.CWD = "", "/work" }),
			errProcessEvidenceListing,
		},
		{
			"native listing metadata",
			withNative(func(opts *Options) {
				opts.CWD, opts.ProjectRoot, opts.ResumeCommand = "/work", "/work", []string{
					"codex",
					"resume",
				}
			}),
			nil,
		},
		{
			"process cwd with start identity",
			withProcess(func(opts *Options) { opts.CWD = "/work" }),
			nil,
		},
		{
			"process automatic cwd",
			withProcess(func(opts *Options) { opts.CWD, opts.CWDAuto = "/work", true }),
			nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Prepare(nil, test.options, Runtime{})
			if test.want == nil {
				if err != nil {
					t.Fatalf("error = %v, want none", err)
				}

				return
			}

			var inputErr *InputError
			if !errors.Is(err, test.want) || !errors.As(err, &inputErr) {
				t.Fatalf("error = %v, want input error %v", err, test.want)
			}
		})
	}
}
