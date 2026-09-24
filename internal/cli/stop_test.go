package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
)

func TestStopExplicitSkippedTargetReturnsReasonAndError(t *testing.T) {
	path, session := createSkippedStopSession(t)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", shortRegistryID(session.ID), "--dry-run"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errStopTargetSkipped) {
		t.Fatalf("single skipped stop error = %v", err)
	}
	if !strings.Contains(stdout.String(), "process no longer exists") || !strings.Contains(stdout.String(), "skipped=1") {
		t.Fatalf("single stop output = %q", stdout.String())
	}
}

func TestStopAllKeepsSkippedResultsMachineReadable(t *testing.T) {
	path, _ := createSkippedStopSession(t)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "stop", "--all", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["stoppable"] != float64(0) || result["skipped"] != float64(1) || result["dry_run"] != true {
		t.Fatalf("stop JSON = %q, %v", stdout.String(), err)
	}
	if _, exists := result["Stoppable"]; exists {
		t.Fatalf("stop JSON retained exported Go field casing: %q", stdout.String())
	}
}

func TestStopRejectsInvalidSelection(t *testing.T) {
	path, session := createSkippedStopSession(t)
	for _, args := range [][]string{{"stop"}, {"stop", shortRegistryID(session.ID), "--all"}} {
		if err := runTestCLI(context.Background(), append([]string{"--store", path}, args...), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid stop selection accepted: %v", args)
		}
	}
}

func TestStopMultipleSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	now := time.Now().UTC()
	live := registry.PresenceLive
	s1, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: now, Subject: registry.ObservationIdentity{SessionID: "session-1"}, Evidence: &registry.Report{Event: "start", Claim: &live}})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: now, Subject: registry.ObservationIdentity{SessionID: "session-2"}, Evidence: &registry.Report{Event: "start", Claim: &live}})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", shortRegistryID(s1.ID), shortRegistryID(s2.ID), "--dry-run"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errStopTargetSkipped) {
		t.Fatalf("expected skipped error, got %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "skipped=2") || !strings.Contains(output, "no stop target") {
		t.Fatalf("stop output missing both session results:\n%s", output)
	}
}

func TestStopAllConfirmationHandling(t *testing.T) {
	path, _ := createSkippedStopSession(t)

	// Refused / non-interactive confirmation without --yes fails with error
	var stdout, stderr bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--store", path, "stop", "--all"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected unconfirmed stop error in non-TTY")
	}

	// Accepted via -y flag
	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", "--all", "-y"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected successful stop with -y, got %v", err)
	}

	// Accepted via --yes flag
	stdout.Reset()

	if err := runTestCLI(context.Background(), []string{"--store", path, "stop", "--all", "--yes"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected successful stop with --yes, got %v", err)
	}
}

func createSkippedStopSession(t *testing.T) (string, registry.Session) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	present := true
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now(), Subject: registry.ObservationIdentity{SessionID: "stop-session"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 1_000_000_000, StartIdentity: "missing:1000000000"}, Present: present}})
	if err != nil {
		t.Fatal(err)
	}
	return path, session
}

var errTestSignal = errors.New("signal failed")

type recordingStopSignaler struct {
	validation  stopTargetValidation
	validateErr error
	validate    func(registry.Session, stopTarget) (stopTargetValidation, error)
	validated   []string
	sendErr     error
	pids        []int
	panes       []string
	servers     []string
}

func (signaler *recordingStopSignaler) ValidateStopTarget(_ context.Context, session registry.Session, target stopTarget) (stopTargetValidation, error) {
	signaler.validated = append(signaler.validated, session.ID)
	if signaler.validate != nil {
		return signaler.validate(session, target)
	}
	return signaler.validation, signaler.validateErr
}

func (signaler *recordingStopSignaler) SendMultiplexerInterrupt(_ context.Context, serverIdentity, paneID string) error {
	signaler.servers = append(signaler.servers, serverIdentity)
	signaler.panes = append(signaler.panes, paneID)
	return signaler.sendErr
}

func (signaler *recordingStopSignaler) SendProcessInterrupt(pid int) error {
	signaler.pids = append(signaler.pids, pid)
	return signaler.sendErr
}

func TestRunManageStopSessionsStopsUniqueValidatedLiveTargets(t *testing.T) {
	t.Parallel()
	signaler := &recordingStopSignaler{validation: stopTargetValidation{OK: true}}
	sessions := []registry.Session{
		{
			ID:       "a",
			Harness:  registry.Harness("codex"),
			Process:  &registry.ProcessIdentity{PID: 101},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
		{
			ID:       "b",
			Harness:  registry.Harness("claude"),
			Location: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "-L:custom", PaneID: "%2"},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
		{
			ID:       "c",
			Harness:  registry.Harness("codex"),
			Process:  &registry.ProcessIdentity{PID: 101},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
		{
			ID:       "d",
			Harness:  registry.Harness("codex"),
			Process:  &registry.ProcessIdentity{PID: 202},
			Liveness: registry.NewLiveness(registry.PresenceGone, registry.ActivityValue(nil), nil),
		},
		{
			ID:       "e",
			Harness:  registry.Harness("claude"),
			Location: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "-L:other", PaneID: "%2"},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
	}
	result, err := runManageStopSessions(context.Background(), sessions, manageStopAllOptions{signaler: signaler})
	if err != nil {
		t.Fatal(err)
	}
	requireStopSummary(t, result)
	requireStopSignals(t, signaler)
}

func requireStopSummary(t *testing.T, result manageStopAllResult) {
	t.Helper()
	if result.Stoppable != 3 || result.Stopped != 3 || result.Skipped != 2 || result.Failed != 0 {
		t.Fatalf("stop result = %+v", result)
	}
}

func requireStopSignals(t *testing.T, signaler *recordingStopSignaler) {
	t.Helper()
	if !slices.Equal(signaler.pids, []int{101}) || !slices.Equal(signaler.panes, []string{"%2", "%2"}) {
		t.Fatalf("signals: pids=%v panes=%v", signaler.pids, signaler.panes)
	}
	if !slices.Equal(signaler.servers, []string{"-L:custom", "-L:other"}) {
		t.Fatalf("tmux server identities = %v", signaler.servers)
	}
}

func TestTmuxStopTargetValidationChecksEveryServer(t *testing.T) {
	t.Parallel()

	session := registry.Session{Location: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "/tmp/correct", PaneID: "%1", PanePID: 42}}
	panes := []tmux.Pane{
		{Tmux: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "/tmp/wrong", PaneID: "%1", PanePID: 41}},
		{Tmux: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "/tmp/correct", PaneID: "%1", PanePID: 42}},
	}
	if validation := tmuxStopTargetValidation(session, panes); !validation.OK {
		t.Fatalf("matching pane on later server was rejected: %#v", validation)
	}
}

func TestTmuxStopTargetRejectsMissingStoredServerIdentity(t *testing.T) {
	t.Parallel()

	session := registry.Session{Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%1", PanePID: 42}}
	panes := []tmux.Pane{
		{Tmux: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "-L:custom", PaneID: "%1", PanePID: 42}},
	}
	if validation := tmuxStopTargetValidation(session, panes); validation.OK {
		t.Fatalf("missing stored server identity approved a custom-server pane: %#v", validation)
	}
	if target, ok := stopTargetForSession(session); ok {
		t.Fatalf("missing server identity produced unsafe target: %#v", target)
	}
	session.Process = &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	target, ok := stopTargetForSession(session)
	if !ok || target.Method != "pid-interrupt" || target.PID != 42 {
		t.Fatalf("missing server identity did not fall back to process: %#v, %t", target, ok)
	}
}

func TestTmuxStopTargetRejectsBackgroundAgent(t *testing.T) {
	t.Parallel()

	loc := registry.Location{
		Kind:      registry.MultiplexerTmux,
		ServerID:  "/tmp/aht-test.sock",
		PaneID:    "%3",
		PanePID:   200,
		SessionID: "$1",
		WindowID:  "@1",
	}
	session := registry.Session{
		Location: loc,
		Process: &registry.ProcessIdentity{
			PID:           201,
			StartIdentity: "agent-201",
			Foreground:    false,
		},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
	}
	target, ok := stopTargetForSession(session)
	if !ok || target.Method != "tmux-interrupt" {
		t.Fatalf("unexpected target: %#v", target)
	}
	result := tmuxStopTargetValidation(session, []tmux.Pane{{Tmux: loc, PanePID: 200}})
	if result.OK {
		t.Fatal("unchanged shell pane accepted tmux interrupt even though tracked agent was not foreground")
	}
}

func TestRunManageStopSessionsValidatesBeforeDeduplicating(t *testing.T) {
	t.Parallel()

	signaler := &recordingStopSignaler{
		validate: func(session registry.Session, _ stopTarget) (stopTargetValidation, error) {
			if session.ID == "a-stale" {
				return stopTargetValidation{Reason: "process identity changed"}, nil
			}
			return stopTargetValidation{OK: true}, nil
		},
	}
	sessions := []registry.Session{
		{
			ID:       "a-stale",
			Harness:  registry.Harness("codex"),
			Process:  &registry.ProcessIdentity{PID: 101},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
		{
			ID:       "b-current",
			Harness:  registry.Harness("codex"),
			Process:  &registry.ProcessIdentity{PID: 101},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
	}
	result, err := runManageStopSessions(context.Background(), sessions, manageStopAllOptions{signaler: signaler})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stoppable != 1 || result.Stopped != 1 || result.Skipped != 1 || !slices.Equal(signaler.pids, []int{101}) {
		t.Fatalf("validated deduplication result = %#v, signals=%#v", result, signaler.pids)
	}
}

func TestRunManageStopSessionsDryRunStillValidatesTargets(t *testing.T) {
	t.Parallel()

	signaler := &recordingStopSignaler{validation: stopTargetValidation{Reason: "process identity changed"}}
	sessions := []registry.Session{
		{
			ID:       "stale",
			Harness:  registry.Harness("codex"),
			Process:  &registry.ProcessIdentity{PID: 101},
			Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
	}
	result, err := runManageStopSessions(
		context.Background(),
		sessions,
		manageStopAllOptions{dryRun: true, signaler: signaler},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stoppable != 0 || result.Skipped != 1 || len(result.Results) != 1 || result.Results[0].Status != "skipped" {
		t.Fatalf("dry-run validation result = %#v", result)
	}
	if !slices.Equal(signaler.validated, []string{"stale"}) || len(signaler.pids) != 0 || len(signaler.panes) != 0 {
		t.Fatalf("dry-run validation/signals = validated %v pids %v panes %v", signaler.validated, signaler.pids, signaler.panes)
	}
}

func TestRunManageStopSessionsReportsSignalFailure(t *testing.T) {
	t.Parallel()
	signaler := &recordingStopSignaler{validation: stopTargetValidation{OK: true}, sendErr: errTestSignal}
	sessions := []registry.Session{{
		ID:       "a",
		Harness:  registry.Harness("codex"),
		Process:  &registry.ProcessIdentity{PID: 101},
		Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
	}}
	result, err := runManageStopSessions(context.Background(), sessions, manageStopAllOptions{signaler: signaler})
	if !errors.Is(err, errManageStopAllFailed) || result.Failed != 1 || result.Stopped != 0 {
		t.Fatalf("stop failure result = %+v, err=%v", result, err)
	}
}
