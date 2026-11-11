package observer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
)

func multiplexerPanesFromTmux(panes []tmux.Pane) []mux.Pane {
	result := make([]mux.Pane, 0, len(panes))
	for _, pane := range panes {
		result = append(result, pane.ToMuxPane())
	}

	return result
}

func TestObserverCorrelatesZellijEnvironmentIdentityAndDetectsScreen(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 1, ProcessGroupID: 42, Foreground: true, StartIdentity: "boot:42",
		Executable: "/usr/bin/codex", CWD: "/repo",
		Env: map[string]string{"ZELLIJ_SESSION_NAME": "work", "ZELLIJ_PANE_ID": "7"},
	}
	pane := mux.Pane{
		Location: registry.Location{
			Kind: registry.MultiplexerZellij, SessionName: "work", TabID: "3", TabName: "agents",
			PaneID: "terminal_7", PaneCurrentPath: "/repo",
		},
		Command: "codex", CWD: "/repo", Title: "Codex",
	}

	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) {
			return []mux.Pane{pane}, nil
		},
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{Text: "› next task\nContext 63% used", Title: "Codex"}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerZellij ||
		sessions[0].Location.PaneID != "terminal_7" {
		t.Fatalf("zellij session = %#v", sessions)
	}

	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityIdle ||
		sessions[0].Decision() == nil ||
		sessions[0].Decision().Authority != "screen" {
		t.Fatalf("zellij activity = %#v", sessions[0])
	}
}

func TestObserverUsesHerdrForegroundProcessAndSemanticState(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 40, ProcessGroupID: 42, StartIdentity: "boot:42",
		Executable: "/usr/bin/claude", CWD: "/repo",
	}
	waiting := registry.ActivityWaiting
	pane := mux.Pane{
		Location: registry.Location{
			Kind: registry.MultiplexerHerdr, SessionName: "work", WorkspaceID: "w1", TabID: "w1:t1",
			PaneID: "w1:p1", PaneCurrentPath: "/repo", PanePID: 42,
		},
		Processes: []mux.ProcessRef{{PID: 42, ProcessGroupID: 42, Command: "claude", CWD: "/repo"}},
		Activity:  &waiting, StateReason: "herdr_agent_status",
	}
	captureCalled := false

	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) {
			return []mux.Pane{pane}, nil
		},
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			captureCalled = true
			return mux.ScreenSnapshot{}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if captureCalled {
		t.Fatal("observer captured the Herdr buffer despite semantic agent state")
	}

	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerHerdr ||
		sessions[0].Location.PaneID != "w1:p1" {
		t.Fatalf("herdr session = %#v", sessions)
	}

	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityWaiting ||
		sessions[0].Decision() == nil ||
		sessions[0].Decision().Authority != registry.AuthorityScreen ||
		sessions[0].Decision().Reason != "herdr_agent_status" {
		t.Fatalf("herdr activity = %#v", sessions[0])
	}
}

func TestObserverUsesProgramStatusOfTheHarnessOnly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		program     mux.ProgramStatus
		wantCapture bool
	}{
		{
			name: "record of the harness",
			program: mux.ProgramStatus{
				App: "claude-code", State: mux.ProgramStateBlocked, Kind: "permission",
			},
			wantCapture: false,
		},
		{
			name:        "record of another program in the pane",
			program:     mux.ProgramStatus{App: "builder", State: mux.ProgramStateBlocked, Kind: "permission"},
			wantCapture: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := registry.NewJournal(
				filepath.Join(t.TempDir(), "sessions.json"),
				catalog.Rules{},
			)
			process := processinfo.Process{
				PID: 42, PPID: 40, ProcessGroupID: 42, StartIdentity: "boot:42",
				Executable: "/usr/bin/claude", CWD: "/repo", TTY: "/dev/pts/7", Foreground: true,
			}
			pane := mux.Pane{
				Location: registry.Location{
					Kind: registry.MultiplexerTmux, ServerID: "/tmp/tmux.sock", SessionName: "work",
					PaneID: "%7", PaneCurrentPath: "/repo", PanePID: 42, PaneTTY: "/dev/pts/7",
				},
				Processes: []mux.ProcessRef{
					{PID: 42, ProcessGroupID: 42, Command: "claude", CWD: "/repo"},
				},
				ProcessTTY: "/dev/pts/7",
				Program:    &test.program,
			}
			captured := false

			observer := New(Options{
				Store: store,
				ProcessList: func(context.Context) ([]processinfo.Process, error) {
					return []processinfo.Process{process}, nil
				},
				PaneList: func(context.Context) ([]mux.Pane, error) {
					return []mux.Pane{pane}, nil
				},
				ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
					captured = true
					return mux.ScreenSnapshot{}, nil
				},
				CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
				Now:         func() time.Time { return time.Now().UTC() },
			})
			if _, err := observer.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			sessions, err := store.List(context.Background(), registry.Filter{})
			if err != nil || len(sessions) != 1 {
				t.Fatalf("sessions = %#v, %v", sessions, err)
			}

			session := sessions[0]

			if captured != test.wantCapture {
				t.Fatalf("pane captured = %t, want %t", captured, test.wantCapture)
			}

			fromRecord := session.Activity() != nil &&
				*session.Activity() == registry.ActivityWaiting &&
				session.Decision() != nil &&
				session.Decision().Authority == registry.AuthorityTerminal &&
				session.Decision().Reason == "program_status" &&
				session.Detail != nil &&
				session.Detail.Value == registry.ActivityDetailPermission
			if fromRecord == test.wantCapture {
				t.Fatalf("state taken from the record = %t: %#v", fromRecord, session.Liveness)
			}
		})
	}
}

func TestObserverFollowsProgramStatusOverHookReports(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 40, ProcessGroupID: 42, StartIdentity: "boot:42",
		Executable: "/usr/bin/claude", CWD: "/repo", TTY: "/dev/pts/7", Foreground: true,
	}
	record := &mux.ProgramStatus{App: "claude-code", State: mux.ProgramStateIdle, Kind: ""}
	now := time.Now().UTC()

	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) {
			return []mux.Pane{
				{
					Location: registry.Location{
						Kind:            registry.MultiplexerTmux,
						ServerID:        "/tmp/tmux.sock",
						SessionName:     "work",
						PaneID:          "%7",
						PaneCurrentPath: "/repo",
						PanePID:         42,
						PaneTTY:         "/dev/pts/7",
					},
					Processes: []mux.ProcessRef{
						{PID: 42, ProcessGroupID: 42, Command: "claude", CWD: "/repo"},
					},
					ProcessTTY: "/dev/pts/7",
					Program:    record,
				},
			}, nil
		},
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return now },
	})

	running, live := registry.ActivityRunning, registry.PresenceLive
	if _, err := store.Observe(t.Context(), registry.Observation{
		Harness: "claude",
		At:      now,
		Subject: registry.ObservationIdentity{SessionID: "followed"},
		Evidence: &registry.Report{
			Reporter: registry.Reporter{Integration: (catalog.Rules{}).Policy("claude").Reporter},
			Event:    "PreToolUse",
			Claim:    &live,
			Activity: &running,
			Process:  processIdentity(process),
		},
	}); err != nil {
		t.Fatal(err)
	}

	cycle := func() registry.Session {
		t.Helper()

		now = now.Add(time.Second)

		if _, err := observer.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}

		sessions, err := store.List(t.Context(), registry.Filter{})
		if err != nil || len(sessions) != 1 {
			t.Fatalf("sessions = %#v, %v", sessions, err)
		}

		return sessions[0]
	}
	state := func(session registry.Session) string {
		return string(*session.Activity()) + " by " + string(session.Decision().Authority)
	}

	first := cycle()
	if got := state(first); got != "idle by terminal" {
		t.Fatalf("resting record under a running hook report = %s", got)
	}

	if second := cycle(); !second.Observations.Screen.ObservedAt.Equal(
		first.Observations.Screen.ObservedAt,
	) {
		t.Fatal("an unchanged record was written again")
	}

	record = nil

	if got := state(cycle()); got != "running by hook" {
		t.Fatalf("state after the record was cleared = %s", got)
	}
}

type programStatusWatches struct {
	mu      sync.Mutex
	servers []string
	changed func([]mux.Pane)
	stopped int
	results []error
}

func (w *programStatusWatches) watch(
	ctx context.Context,
	_ registry.MultiplexerKind,
	server string,
	changed func([]mux.Pane),
) error {
	w.mu.Lock()
	w.servers = append(w.servers, server)
	w.changed = changed

	var result error

	ended := len(w.results) > 0
	if ended {
		result, w.results = w.results[0], w.results[1:]
	}
	w.mu.Unlock()

	if ended {
		return result
	}

	<-ctx.Done()

	w.mu.Lock()
	w.stopped++
	w.mu.Unlock()

	return nil
}

func (w *programStatusWatches) started() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return slices.Clone(w.servers)
}

type programStatusRun struct {
	store  *registry.MemoryStore
	cycles <-chan Result
	stop   func()
}

func (r programStatusRun) state(t *testing.T) string {
	t.Helper()

	sessions, err := r.store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}

	return string(*sessions[0].Activity()) + " by " + string(sessions[0].Decision().Authority)
}

func runProgramStatusObserver(
	t *testing.T,
	interval time.Duration,
	watches *programStatusWatches,
	panes func() []mux.Pane,
) programStatusRun {
	t.Helper()

	store, err := registry.OpenMemoryStore(
		filepath.Join(t.TempDir(), "state.json"),
		catalog.Rules{},
	)
	if err != nil {
		t.Fatal(err)
	}

	observer := New(Options{
		Store:    store,
		Interval: interval,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{programStatusProcess()}, nil
		},
		PaneList:            func(context.Context) ([]mux.Pane, error) { return panes(), nil },
		CatalogList:         func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		ProgramStatusWatch:  watches.watch,
		ProcessConfirmation: -1,
		Quiet:               true,
	})

	ctx, cancel := context.WithCancel(t.Context())
	results := make(chan Result)
	done := make(chan error, 1)

	go func() {
		done <- observer.RunWithResults(ctx, func(result Result) error {
			select {
			case results <- result:
			case <-ctx.Done():
			}

			return nil
		})
	}()

	return programStatusRun{store: store, cycles: results, stop: func() {
		t.Helper()
		cancel()

		if err := <-done; err != nil {
			t.Error(err)
		}

		_ = store.Close()
	}}
}

func programStatusProcess() processinfo.Process {
	return processinfo.Process{
		PID: 42, PPID: 40, ProcessGroupID: 42, StartIdentity: "boot:42",
		Executable: "/usr/bin/claude", CWD: "/repo", TTY: "/dev/pts/7", Foreground: true,
	}
}

func programStatusPane(server string, tty string, pid int, state mux.ProgramState) mux.Pane {
	pane := mux.Pane{
		Location: registry.Location{
			Kind: registry.MultiplexerTmux, ServerID: server, SessionName: "work",
			PaneID: "%7", PaneCurrentPath: "/repo", PanePID: pid, PaneTTY: tty,
		},
		ProcessTTY: tty,
	}
	if pid != 0 {
		pane.Processes = []mux.ProcessRef{
			{PID: pid, ProcessGroupID: pid, Command: "claude", CWD: "/repo"},
		}
	}

	if state != "" {
		pane.Program = &mux.ProgramStatus{App: "claude-code", State: state, Kind: ""}
	}

	return pane
}

func TestObserverRecordsProgramStatusChangeBetweenCycles(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		watches := &programStatusWatches{}
		run := runProgramStatusObserver(t, time.Hour, watches, func() []mux.Pane {
			return []mux.Pane{
				programStatusPane("/tmp/agents.sock", "/dev/pts/7", 42, mux.ProgramStateIdle),
				programStatusPane("/tmp/shells.sock", "/dev/pts/9", 0, ""),
			}
		})

		<-run.cycles
		synctest.Wait()

		if got := watches.started(); !slices.Equal(got, []string{"/tmp/agents.sock"}) {
			t.Fatalf("watched servers = %v, want only the one with a harness pane", got)
		}

		if got := run.state(t); got != "idle by terminal" {
			t.Fatalf("state after the first cycle = %s", got)
		}

		time.Sleep(time.Second)

		running, live := registry.ActivityRunning, registry.PresenceLive
		if _, err := run.store.Observe(t.Context(), registry.Observation{
			Harness: "claude",
			At:      time.Now(),
			Subject: registry.ObservationIdentity{SessionID: "watched"},
			Evidence: &registry.Report{
				Reporter: registry.Reporter{
					Integration: (catalog.Rules{}).Policy("claude").Reporter,
				},
				Event:    "PreToolUse",
				Claim:    &live,
				Activity: &running,
				Process:  processIdentity(programStatusProcess()),
			},
		}); err != nil {
			t.Fatal(err)
		}

		for _, step := range []struct {
			state mux.ProgramState
			want  string
		}{
			{state: mux.ProgramStateWorking, want: "running by terminal"},
			{state: mux.ProgramStateDone, want: "idle by terminal"},
			{state: "", want: "running by hook"},
		} {
			time.Sleep(time.Second)
			watches.changed([]mux.Pane{
				programStatusPane("/tmp/agents.sock", "/dev/pts/7", 42, step.state),
			})

			if got := run.state(t); got != step.want {
				t.Fatalf(
					"state after the record became %q = %s, want %s",
					step.state,
					got,
					step.want,
				)
			}
		}

		synctest.Wait()

		select {
		case <-run.cycles:
			t.Fatal("a program status change ran a cycle")
		default:
		}

		run.stop()

		if got := watches.started(); len(got) != 1 {
			t.Fatalf("watches started = %v, want one for the server", got)
		}

		if watches.stopped != 1 {
			t.Fatalf("watches stopped with the observer = %d, want 1", watches.stopped)
		}
	})
}

func TestObserverRestartsProgramStatusWatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		watches := &programStatusWatches{
			results: []error{io.ErrUnexpectedEOF, mux.ErrProgramStatusUnsignaled},
		}
		listed := true
		run := runProgramStatusObserver(t, time.Second, watches, func() []mux.Pane {
			if !listed {
				return nil
			}

			return []mux.Pane{programStatusPane("/tmp/agents.sock", "/dev/pts/7", 42, "")}
		})

		cycle := func() int {
			<-run.cycles
			synctest.Wait()

			return len(watches.started())
		}

		if got := cycle(); got != 1 {
			t.Fatalf("watches after the first cycle = %d, want 1", got)
		}

		if got := cycle(); got != 2 {
			t.Fatalf("watches after one ended with an error = %d, want 2", got)
		}

		if got := cycle(); got != 2 {
			t.Fatalf("watches of a server that cannot signal = %d, want 2", got)
		}

		listed = false

		cycle()

		listed = true

		if got := cycle(); got != 3 {
			t.Fatalf("watches after the server came back = %d, want 3", got)
		}

		run.stop()
	})
}

func TestObserverPrefersRicherNestedMultiplexerLocation(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID:            42,
		PPID:           40,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/codex",
		CWD:            "/repo",
		Env:            map[string]string{"ZELLIJ_SESSION_NAME": "outer", "ZELLIJ_PANE_ID": "7"},
	}
	panes := []mux.Pane{
		{
			Location: registry.Location{
				Kind:            registry.MultiplexerZellij,
				SessionName:     "outer",
				PaneID:          "terminal_7",
				PaneCurrentPath: "/repo",
			},
			Command: "codex",
			CWD:     "/repo",
		},
		{
			Location: registry.Location{
				Kind:            registry.MultiplexerHerdr,
				SessionName:     "inner",
				WorkspaceID:     "w1",
				TabID:           "w1:t1",
				PaneID:          "w1:p1",
				PaneCurrentPath: "/repo",
			},
			Processes: []mux.ProcessRef{
				{PID: 42, ProcessGroupID: 42, Command: "codex", CWD: "/repo"},
			},
		},
	}

	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) { return panes, nil },
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{Text: "› next task"}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerHerdr ||
		sessions[0].Location.PaneID != "w1:p1" {
		t.Fatalf("nested location = %#v", sessions)
	}
}

var errStubCapture = errors.New("stub capture failed")

type stubDriver struct {
	mux.Driver

	kind registry.MultiplexerKind
}

func (d stubDriver) CapturePane(_ context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	if pane.Title == "closed" {
		return mux.ScreenSnapshot{}, errStubCapture
	}

	return mux.ScreenSnapshot{Text: string(d.kind) + " " + pane.Location.PaneID, Title: ""}, nil
}

type batchStubDriver struct {
	stubDriver
}

func (d batchStubDriver) CapturePanes(ctx context.Context, panes []mux.Pane) []mux.ScreenCapture {
	captures := captureEachPane(d.CapturePane)(ctx, panes)
	for i := range captures {
		captures[i].Snapshot.Title = fmt.Sprintf("batch of %d", len(panes))
	}

	return captures
}

func TestCaptureDriverPanesKeepsEachPaneWithItsDriver(t *testing.T) {
	t.Parallel()

	pane := func(kind registry.MultiplexerKind, id string, title string) mux.Pane {
		return mux.Pane{
			Location: registry.Location{Kind: kind, PaneID: id},
			Command:  "",
			CWD:      "",
			Title:    title,
		}
	}
	panes := []mux.Pane{
		pane("batching", "a1", ""),
		pane("single", "b1", ""),
		pane("unknown", "x1", ""),
		pane("batching", "a2", "closed"),
		pane("single", "b2", "closed"),
		pane("batching", "a3", ""),
		pane("single", "b3", ""),
	}

	drivers := map[registry.MultiplexerKind]mux.Driver{
		"batching": batchStubDriver{stubDriver{Driver: nil, kind: "batching"}},
		"single":   stubDriver{Driver: nil, kind: "single"},
	}
	find := func(kind registry.MultiplexerKind) (mux.Driver, bool) {
		driver, ok := drivers[kind]

		return driver, ok
	}

	captures := captureDriverPanes(context.Background(), panes, find)

	want := []struct {
		text, title string
		err         error
	}{
		{text: "batching a1", title: "batch of 3", err: nil},
		{text: "single b1", title: "", err: nil},
		{text: "", title: "", err: errUnsupportedMultiplexerPane},
		{text: "", title: "", err: errStubCapture},
		{text: "", title: "", err: errStubCapture},
		{text: "batching a3", title: "batch of 3", err: nil},
		{text: "single b3", title: "", err: nil},
	}
	if len(captures) != len(want) {
		t.Fatalf("captures = %d, want %d", len(captures), len(want))
	}

	for i, capture := range captures {
		if capture.Snapshot.Text != want[i].text || capture.Snapshot.Title != want[i].title ||
			!errors.Is(capture.Err, want[i].err) {
			t.Errorf("pane %s capture = %#v, want %#v", panes[i].Location.PaneID, capture, want[i])
		}
	}
}

func TestCommandCWDFallbackRequiresUniquePane(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{
		PID:           42,
		StartIdentity: "boot:42",
		Executable:    "/usr/bin/codex",
		CWD:           "/repo",
	}
	panes := []mux.Pane{
		{
			Location: registry.Location{
				Kind:        registry.MultiplexerZellij,
				SessionName: "one",
				PaneID:      "terminal_1",
			},
			Command: "codex",
			CWD:     "/repo",
		},
		{
			Location: registry.Location{
				Kind:        registry.MultiplexerZellij,
				SessionName: "two",
				PaneID:      "terminal_2",
			},
			Command: "codex",
			CWD:     "/repo",
		},
	}
	counts := commandPaneCounts(panes)
	byPID := map[int]processinfo.Process{42: process}

	harnessByPID := map[int]registry.Harness{42: registry.Harness("codex")}
	if _, _, ok := multiplexerPaneProcess(
		panes[0],
		[]processinfo.Process{process},
		newProcessTree(byPID),
		harnessByPID,
		counts,
	); ok {
		t.Fatal("ambiguous command/cwd fallback correlated a pane")
	}
}
