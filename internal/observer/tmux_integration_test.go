//go:build integration

package observer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	harnesspkg "github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/internal/testtmux"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
)

//nolint:cyclop,gocognit // end-to-end setup and assertions intentionally cover all four agents in one server
func TestRealTmuxBottomScreenDetectionForFourAgents(t *testing.T) {
	if testing.Short() {
		t.Skip("real tmux integration test")
	}
	var server *testtmux.Server
	ctx := context.Background()

	tests := []struct {
		harness registry.Harness
		screen  string
		want    registry.Activity
	}{
		{registry.HarnessCodex, "Would you like to run the following command?", registry.ActivityWaiting},
		{registry.HarnessClaude, "Thinking… esc to interrupt", registry.ActivityRunning},
		{registry.HarnessOpenCode, "Ask anything", registry.ActivityIdle},
		{registry.HarnessPi, "Type a message · Enter to send", registry.ActivityIdle},
	}
	processes := make([]processinfo.Process, 0, len(tests))
	panes := make([]tmux.Pane, 0, len(tests))
	for index, test := range tests {
		sessionName := string(test.harness)
		script := filepath.Join(t.TempDir(), sessionName+".sh")
		contents := "#!/bin/sh\nprintf '\\033[999;1H%s' " + harnesspkg.ShellQuote(test.screen) + "\nexec sleep 60\n"
		if err := os.WriteFile(script, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(script, 0o700); err != nil {
			t.Fatal(err)
		}
		if server == nil {
			server = testtmux.New(t, "-s", sessionName, script)
		} else {
			_, err := server.Tmux.NewSession(ctx, gotmux.NewSessionOptions{ //nolint:exhaustruct_v5 // remaining options default
				Name:    sessionName,
				Program: gotmux.Shell(script),
			})
			if err != nil {
				t.Fatalf("create test session %s: %v", sessionName, err)
			}
		}
		sess, err := server.Tmux.FindSession(ctx, sessionName)
		if err != nil {
			t.Fatalf("find session %s: %v", sessionName, err)
		}
		links, err := sess.Windows(ctx)
		if err != nil || len(links) == 0 {
			t.Fatalf("windows for %s: %v", sessionName, err)
		}
		activePane, err := links[0].Window().ActivePane(ctx)
		if err != nil {
			t.Fatalf("active pane for %s: %v", sessionName, err)
		}
		info, err := activePane.Info(ctx)
		if err != nil {
			t.Fatalf("pane info for %s: %v", sessionName, err)
		}
		paneID := string(info.ID)
		paneTTY := info.TTY
		panePID := info.PID
		processPID := 5000 + index
		processes = append(processes, processinfo.Process{PID: processPID, PPID: panePID, ProcessGroupID: processPID, Foreground: true, StartIdentity: "test:" + sessionName, Executable: "/usr/bin/" + sessionName, CWD: "/tmp", TTY: paneTTY, Args: []string{sessionName}})
		tmuxCtx := registry.TmuxContext{Inside: true, ServerSocket: server.Socket, SessionID: string(sess.ID()), SessionName: sessionName, WindowID: string(info.WindowID), WindowIndex: "0", WindowName: sessionName, PaneID: paneID, PaneIndex: "0", PaneCurrentPath: "/tmp", PanePID: panePID, PaneTTY: paneTTY}
		pane := tmux.Pane{Tmux: tmuxCtx, ServerIdentity: server.Socket, PanePID: panePID, PaneTTY: paneTTY}
		panes = append(panes, pane)
		deadline := time.Now().Add(2 * time.Second)
		for {
			snapshot, captureErr := tmux.CapturePane(ctx, pane)
			if captureErr == nil && strings.Contains(snapshot.Text, test.screen) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("tmux pane %s did not render fixture %q: snapshot=%#v error=%v", paneID, test.screen, snapshot, captureErr)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	observer := New(Options{Store: store, ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil }, PaneList: func(context.Context) ([]mux.Pane, error) { return multiplexerPanesFromTmux(panes), nil }, CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil }, DetectionConfigDir: t.TempDir(), Now: func() time.Time { return time.Now().UTC() }})
	result, err := observer.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Degraded {
		t.Fatalf("real tmux observer degraded: %#v", result)
	}
	sessions, err := store.List(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != len(tests) {
		t.Fatalf("sessions = %d, want %d: %#v", len(sessions), len(tests), sessions)
	}
	wantByHarness := make(map[registry.Harness]registry.Activity, len(tests))
	for _, test := range tests {
		wantByHarness[test.harness] = test.want
	}
	for _, session := range sessions {
		want := wantByHarness[session.Harness]
		if session.Activity == nil || *session.Activity != want || session.ActivityDecision == nil || session.ActivityDecision.Authority != "screen" {
			t.Errorf("session %s activity=%s screen=%#v, want %s screen activity", session.Harness, activityValue(session.Activity), *session.Observations.Screen, want)
		}
	}

	raceOptions := Options{Store: store, ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil }, PaneList: func(context.Context) ([]mux.Pane, error) { return multiplexerPanesFromTmux(panes), nil }, CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil }, DetectionConfigDir: t.TempDir(), Now: func() time.Time { return time.Now().UTC() }}
	raceOptions.ScreenCapture = func(captureCtx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
		snapshot, captureErr := captureMultiplexerPane(captureCtx, pane)
		if captureErr != nil {
			return mux.ScreenSnapshot{}, fmt.Errorf("capture race fixture: %w", captureErr)
		}
		harnessID := registry.Harness(pane.Location.SessionName)
		if harnessID != registry.HarnessPi && harnessID != registry.HarnessOpenCode {
			return snapshot, nil
		}
		integration := "pi-extension"
		if harnessID == registry.HarnessOpenCode {
			integration = "opencode-plugin"
		}
		for _, process := range processes {
			if process.TTY != pane.ProcessTTY {
				continue
			}
			running := registry.ActivityRunning
			presence := registry.PresenceLive
			if _, err := store.Observe(captureCtx, registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Harness: harnessID, Identity: registry.ObservationIdentity{SessionID: "race-" + string(harnessID)}, Presence: &presence, Activity: &running, NativeEvent: "integration_race", Process: processIdentity(process), Attributes: map[string]string{"aht_integration": integration}, ObservedAt: time.Now().UTC()}); err != nil {
				return mux.ScreenSnapshot{}, fmt.Errorf("record integration race: %w", err)
			}
			break
		}
		return snapshot, nil
	}
	if raceResult, err := New(raceOptions).RunOnce(ctx); err != nil || raceResult.Degraded {
		t.Fatalf("real tmux race reconciliation = %#v, %v", raceResult, err)
	}
	sessions, err = store.List(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.Harness != registry.HarnessPi && session.Harness != registry.HarnessOpenCode {
			continue
		}
		if session.Activity == nil || *session.Activity != registry.ActivityRunning || session.ActivityDecision == nil || session.ActivityDecision.Authority != "hook" {
			t.Errorf("real tmux race allowed fallback to overwrite %s integration: %#v", session.Harness, session)
		}
	}
}

func activityValue(value *registry.Activity) registry.Activity {
	if value == nil {
		return ""
	}
	return *value
}
