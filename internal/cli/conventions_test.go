package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/urfave/cli/v3"

	"github.com/zigai/aht/internal/config"
	"github.com/zigai/aht/pkg/registry"
)

func TestCommandsRejectUnexpectedArgumentsBeforeSideEffects(t *testing.T) {
	for _, args := range [][]string{
		{"list"},
		{"watch"},
		{"manage", "doctor"},
		{"manage", "state", "path"},
		{"manage", "state", "reset", "--force"},
		{"manage", "state", "clean", "--all", "--yes"},
		{"manage", "tracker", "run", "--once"},
		{"manage", "tracker", "enable"},
		{"manage", "tracker", "disable"},
		{"manage", "tracker", "status"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "missing-config.toml")
			err := runTestCLI(t.Context(), append(append([]string{"--config", configPath}, args...), "unexpected-argument"), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || (!strings.Contains(err.Error(), "unexpected-argument") && !strings.Contains(err.Error(), "unexpected argument")) {
				t.Fatalf("argument error = %v", err)
			}
			if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("argument validation touched config: %v", err)
			}
		})
	}
}

func TestSetupValidatesSelectionBeforeConfigOrInstallation(t *testing.T) {
	err := runTestCLI(t.Context(), []string{"--config", filepath.Join(t.TempDir(), "missing.toml"), "manage", "setup", "not-a-harness"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "normalize agent") {
		t.Fatalf("setup validation error = %v", err)
	}
}

func TestListSummaryAcceptsDefaultSortingConfig(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var stdout, stderr bytes.Buffer
	err := runTestCLI(t.Context(), []string{"--store", storePath, "--json", "list", "--summary"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("default sorting invalidated summary: %v; stderr=%s", err, stderr.String())
	}

	err = runTestCLI(t.Context(), []string{"--store", storePath, "--json", "list", "--summary", "--sort", "updated"}, &stdout, &stderr)
	if !errors.Is(err, errListSummaryFlag) {
		t.Fatalf("explicit summary sorting error = %v", err)
	}
}

func TestServiceOptionsUseConfigAndExplicitOverrides(t *testing.T) {
	app := &application{cfgLoaded: true, cfg: config.Config{Tracker: config.TrackerConfig{Interval: "2s", GracePeriod: "5s"}}}
	cmd := &cli.Command{
		Flags: []cli.Flag{
			&cli.DurationFlag{Name: "interval", Value: time.Second},
		},
	}
	options := serviceOptions{binary: "/aht", interval: time.Second}
	got, err := app.configuredServiceOptions(cmd, options)
	if err != nil || got.Interval != 2*time.Second || got.GracePeriod != 5*time.Second {
		t.Fatalf("configured service = %#v, %v", got, err)
	}

	cmd2 := &cli.Command{
		Flags: []cli.Flag{
			&cli.DurationFlag{Name: "interval", Value: time.Second},
		},
	}
	_ = cmd2.Run(t.Context(), []string{"test", "--interval", "1s"})
	got, err = app.configuredServiceOptions(cmd2, options)
	if err != nil || got.Interval != time.Second || got.GracePeriod != 5*time.Second {
		t.Fatalf("overridden service = %#v, %v", got, err)
	}
}

func TestCleanAllRequiresConfirmationAndPreservesRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	presence := registry.PresenceGone
	if _, err := store.Observe(t.Context(), registry.Observation{Harness: registry.HarnessCodex, Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Identity: registry.ObservationIdentity{SessionID: "gone"}, Presence: &presence, ObservedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	// 1. Without --yes in non-TTY environment (e.g. piped or automated test): should fail and preserve records
	var stdout, stderr bytes.Buffer
	err := runTestCLI(t.Context(), []string{"--store", path, "manage", "state", "clean", "--all"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unconfirmed clean in non-TTY")
	}
	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 || stdout.Len() != 0 {
		t.Fatalf("unconfirmed clean changed state/output: %v, %#v, %q", err, sessions, stdout.String())
	}

	// 2. With --all --yes: should succeed and remove records
	stdout.Reset()
	stderr.Reset()
	err = runTestCLI(t.Context(), []string{"--store", path, "manage", "state", "clean", "--all", "--yes"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("confirmed clean failed: %v", err)
	}
	sessions, err = store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("confirmed clean did not remove records: %#v, %v", sessions, err)
	}
}

func TestFilesystemWatchPreservesRequestedFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	for _, harness := range []registry.Harness{registry.HarnessCodex, registry.HarnessClaude} {
		if _, err := store.Observe(t.Context(), registry.Observation{Harness: harness, Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Identity: registry.ObservationIdentity{SessionID: string(harness)}, NativeEvent: "session_start", ObservedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	var stdout bytes.Buffer
	app := &application{storePath: path, stdout: &stdout}
	watcher := startTestWatch(t, func(ctx context.Context, ready chan struct{}) error {
		return app.runFilesystemWatch(ctx, watchOptions{filter: registry.Filter{Harness: registry.HarnessCodex}, format: watchFormatJSON, ready: ready, now: time.Now})
	})
	watcher.stop(t)
	var event watchEvent
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &event); err != nil || event.Harness != registry.HarnessCodex {
		t.Fatalf("filtered snapshot = %q, %v", stdout.String(), err)
	}
}

func TestListColumnsPreservePrimaryValuesAndUseCellWidths(t *testing.T) {
	//nolint:gosmopolitan // Test fixture intentionally uses multibyte CJK characters to verify cell width calculations.
	row := []string{"kimi-code:123456789abcdef", "kimi-code", "session", "live", "interrupted", "location", "日本語", "now"}
	columns := listTableColumns([][]string{row}, 200)
	for _, index := range []int{0, 1, 3, 4, 6} {
		if columns[index].width < text.StringWidth(row[index]) {
			t.Errorf("column %s width %d truncates %q", columns[index].heading, columns[index].width, row[index])
		}
	}
}

func TestDisabledScreenInspectionDoesNotCaptureLivePane(t *testing.T) {
	session := registry.Session{Harness: registry.HarnessCodex, Multiplexer: registry.MultiplexerContext{Kind: registry.MultiplexerTmux, PaneID: "%1"}}
	result, err := evaluateExplanation(t.Context(), session, infoOptions{disableScreenInspection: true})
	if err != nil || result.Screen.Evaluated || result.Screen.UnavailableReason != "screen_inspection_disabled" {
		t.Fatalf("disabled screen explanation = %#v, %v", result.Screen, err)
	}
}

func TestStopResultsPreserveCompleteTargetsAndErrors(t *testing.T) {
	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	id := "kimi-code:1234567890abcdef1234567890abcdef"
	target := "/run/user/1000/tmux-long-server-identity:%123456"
	message := "permission denied while signaling the selected process; check the process owner before retrying"
	result := manageStopAllResult{Failed: 1, Results: []manageStopSessionResult{{ID: id, Harness: registry.HarnessKimiCode, Target: target, Error: message, Status: "failed"}}}
	if err := app.writeManageStopAllResult(result); err != nil {
		t.Fatal(err)
	}
	compact := strings.Join(strings.Fields(stdout.String()), "")
	for _, value := range []string{id, target, message} {
		if !strings.Contains(compact, strings.Join(strings.Fields(value), "")) {
			t.Errorf("stop result lost %q: %q", value, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "…") {
		t.Fatalf("stop output truncated actionable data: %q", stdout.String())
	}
}

func TestWatchOutputPreservesLongSessionLabels(t *testing.T) {
	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	label := strings.Repeat("long-session-identifier/", 12)
	event := watchEvent{Time: time.Now(), Action: watchActionSnapshot, Harness: registry.HarnessCodex, Label: label}
	writer := watchEventWriter{app: app, format: watchFormatTable}
	if err := writer.write([]watchEvent{event}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(stdout.String()), ""), label) {
		t.Fatalf("table watch lost session label: %q", stdout.String())
	}
	assertHumanLinesBounded(t, stdout.String())
	if output := formatWatchPlainEvent(event); !strings.Contains(output, label) {
		t.Fatalf("plain watch lost session label: %q", output)
	}
}

func TestSummaryIncludesFailureStatesAndServerIdentity(t *testing.T) {
	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	if err := app.writeSummaryTable([]registry.Summary{{MultiplexerKind: registry.MultiplexerTmux, MultiplexerServerID: "server-one", MultiplexerSessionID: "$0", Failed: 2, Interrupted: 3, Total: 5, Live: 5}}, false); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"server-one", "$0", "Failed", "Interrupted", "2", "3"} {
		if !strings.Contains(stdout.String(), value) {
			t.Errorf("summary missing %q: %q", value, stdout.String())
		}
	}
}
