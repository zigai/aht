package zellij_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/zellij"
)

var (
	errBinaryNotFound = errors.New("binary not found")
	errSessionBroken  = errors.New("session is broken")
)

const (
	contractDir  = "contract_version_1"
	paneListJSON = `[{"id":7,"is_plugin":false,"title":"Codex","exited":false,"tab_id":3,"tab_position":1,"tab_name":"agents","pane_command":"codex","pane_cwd":"/repo"},
		{"id":8,"is_plugin":true,"title":"status","exited":false},
		{"id":9,"is_plugin":false,"title":"old","exited":true}]`
)

var testNow = time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)

// sessionServer stands in for a Zellij server: a listening session socket that
// counts the connections clients make to it.
type sessionServer struct {
	path     string
	probes   atomic.Int32
	sentinel chan struct{}
}

// startSessionServer binds a session socket whose modification time is created.
func startSessionServer(t *testing.T, socketDir string, name string, created time.Time) *sessionServer {
	t.Helper()
	dir := filepath.Join(socketDir, contractDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := &sessionServer{path: filepath.Join(dir, name), sentinel: make(chan struct{})}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", server.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(server.path, created, created); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			data, _ := io.ReadAll(conn)
			_ = conn.Close()
			if string(data) == "sentinel" {
				close(server.sentinel)
				continue
			}
			server.probes.Add(1)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return server
}

// probeCount returns how many connections clients have made so far. The server
// accepts connections in order, so once it has accepted a sentinel connection
// made here, every earlier connection has been counted.
func (s *sessionServer) probeCount(t *testing.T) int {
	t.Helper()
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "unix", s.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("sentinel")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.sentinel:
	case <-time.After(5 * time.Second):
		t.Fatal("session server did not accept the sentinel connection")
	}
	return int(s.probes.Load())
}

// writeMetadata creates the session metadata file Zellij writes once a session
// is initialized, with the given modification time.
func writeMetadata(t *testing.T, sessionInfoDir string, name string, modified time.Time) {
	t.Helper()
	dir := filepath.Join(sessionInfoDir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session-metadata.kdl")
	if err := os.WriteFile(path, []byte("name \""+name+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

type scopedCall struct {
	args     []string
	sessions []string
}

// scopedRunner records each command with the sessions visible in the socket
// directory it was given, and answers with the output registered for the
// --session argument.
func visibleSessions(socketDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(socketDir, contractDir))
	if err != nil {
		return nil, fmt.Errorf("read scoped socket dir: %w", err)
	}
	var sessions []string
	for _, entry := range entries {
		sessions = append(sessions, entry.Name())
	}
	return sessions, nil
}

func scopedRunner(calls *[]scopedCall, outputs map[string]string, failures map[string]error) zellij.CommandRunner {
	return func(ctx context.Context, args ...string) (string, error) {
		sessions, err := visibleSessions(zellij.SocketDir(ctx))
		if err != nil {
			return "", err
		}
		*calls = append(*calls, scopedCall{args: slices.Clone(args), sessions: sessions})
		session := args[1]
		if failure := failures[session]; failure != nil {
			return "", failure
		}
		return outputs[session], nil
	}
}

func sessionNames(panes []mux.Pane) []string {
	var names []string
	for _, pane := range panes {
		if !slices.Contains(names, pane.Location.SessionName) {
			names = append(names, pane.Location.SessionName)
		}
	}
	slices.Sort(names)
	return names
}

func TestCurrentWithEnvNormalizesTerminalPaneID(t *testing.T) {
	t.Parallel()
	location := zellij.CurrentWithEnv(zellij.Env{SessionName: "work", PaneID: "7"})
	if location.Kind != registry.MultiplexerZellij || location.SessionName != "work" || location.PaneID != "terminal_7" {
		t.Fatalf("CurrentWithEnv() = %#v", location)
	}
	if got := zellij.CurrentWithEnv(zellij.Env{SessionName: "work"}); !got.Empty() {
		t.Fatalf("incomplete environment produced context: %#v", got)
	}
}

func TestCapturePaneTargetsNativePaneAndBoundsOutput(t *testing.T) {
	t.Parallel()
	socketDir := t.TempDir()
	startSessionServer(t, socketDir, "work", testNow.Add(-time.Hour))
	startSessionServer(t, socketDir, "boot", testNow)
	var got []string
	var visible []string
	lines := make([]string, 101)
	for index := range lines {
		lines[index] = fmt.Sprintf("line-%03d", index)
	}
	snapshot, err := zellij.CapturePaneWithOptions(context.Background(), mux.Pane{
		Location: registry.Location{Kind: registry.MultiplexerZellij, SessionName: "work", PaneID: "terminal_7"},
		Title:    "Codex",
	}, zellij.CaptureOptions{SocketDir: socketDir, Run: func(ctx context.Context, args ...string) (string, error) {
		got = append([]string(nil), args...)
		var err error
		visible, err = visibleSessions(zellij.SocketDir(ctx))
		if err != nil {
			return "", err
		}
		return strings.Join(lines, "\n") + "\n", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--session", "work", "action", "dump-screen", "--pane-id", "terminal_7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capture args = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(visible, []string{"work"}) {
		t.Fatalf("capture command saw sessions %v, want only the pane's session", visible)
	}
	wantText := strings.Join(lines[1:], "\n")
	if snapshot.Text != wantText || snapshot.Title != "Codex" {
		t.Fatalf("snapshot text = %q, want %q; title = %q", snapshot.Text, wantText, snapshot.Title)
	}
}

func TestListPanesUsesNativeJSONInventory(t *testing.T) {
	t.Parallel()
	socketDir, infoDir := t.TempDir(), t.TempDir()
	startSessionServer(t, socketDir, "work", testNow.Add(-time.Hour))
	var calls []scopedCall
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
		Run:       scopedRunner(&calls, map[string]string{"work": paneListJSON}, nil),
		SocketDir: socketDir, SessionInfoDir: infoDir, Now: func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 {
		t.Fatalf("panes = %#v", panes)
	}
	pane := panes[0]
	if pane.Location.Kind != registry.MultiplexerZellij || pane.Location.SessionName != "work" || pane.Location.TabID != "3" || pane.Location.TabIndex != "1" || pane.Location.PaneID != "terminal_7" || pane.CWD != "/repo" || pane.Command != "codex" {
		t.Fatalf("pane = %#v", pane)
	}
	want := []scopedCall{{args: []string{"--session", "work", "action", "list-panes", "--all", "--json"}, sessions: []string{"work"}}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestListPanesNeverConnectsToStartingSession(t *testing.T) {
	t.Parallel()
	socketDir, infoDir := t.TempDir(), t.TempDir()
	startSessionServer(t, socketDir, "work", testNow.Add(-time.Hour))
	starting := startSessionServer(t, socketDir, "boot", testNow.Add(-time.Second))
	var calls []scopedCall
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
		Run:       scopedRunner(&calls, map[string]string{"work": paneListJSON, "boot": paneListJSON}, nil),
		SocketDir: socketDir, SessionInfoDir: infoDir, Now: func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionNames(panes); !reflect.DeepEqual(got, []string{"work"}) {
		t.Fatalf("sessions = %v, want only the established session", got)
	}
	for _, call := range calls {
		if !reflect.DeepEqual(call.sessions, []string{"work"}) {
			t.Fatalf("zellij command saw sessions %v, want only the session it targets", call.sessions)
		}
	}
	if probes := starting.probeCount(t); probes != 0 {
		t.Fatalf("starting session received %d connections, want 0", probes)
	}
}

func TestListPanesDecidesSessionReadiness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		age      time.Duration
		metadata *time.Duration
		want     []string
	}{
		{name: "young without metadata", age: time.Second, want: nil},
		{name: "young with metadata from an earlier session", age: time.Second, metadata: new(-time.Minute), want: nil},
		{name: "young with metadata written after the socket", age: 3 * time.Second, metadata: new(-time.Second), want: []string{"s"}},
		{name: "old without metadata", age: 11 * time.Second, want: []string{"s"}},
		{name: "old with stale metadata", age: time.Hour, metadata: new(-2 * time.Hour), want: []string{"s"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			socketDir, infoDir := t.TempDir(), t.TempDir()
			startSessionServer(t, socketDir, "s", testNow.Add(-test.age))
			if test.metadata != nil {
				writeMetadata(t, infoDir, "s", testNow.Add(*test.metadata))
			}
			var calls []scopedCall
			panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
				Run:       scopedRunner(&calls, map[string]string{"s": paneListJSON}, nil),
				SocketDir: socketDir, SessionInfoDir: infoDir, Now: func() time.Time { return testNow },
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := sessionNames(panes); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("sessions = %v, want %v", got, test.want)
			}
		})
	}
}

func TestListPanesIgnoresSocketsThatAreNotLiveSessions(t *testing.T) {
	t.Parallel()
	socketDir, infoDir := t.TempDir(), t.TempDir()
	old := testNow.Add(-time.Hour)
	startSessionServer(t, socketDir, "web_server_bus", old)
	stale := startSessionServer(t, socketDir, "crashed", old)
	if err := os.Remove(stale.path); err != nil {
		t.Fatal(err)
	}
	staleListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: stale.path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	staleListener.SetUnlinkOnClose(false)
	if err := staleListener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale.path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(socketDir, contractDir, "notes"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []scopedCall
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
		Run:       scopedRunner(&calls, nil, nil),
		SocketDir: socketDir, SessionInfoDir: infoDir, Now: func() time.Time { return testNow },
	})
	if err != nil || len(panes) != 0 || len(calls) != 0 {
		t.Fatalf("ListPanesWithOptions() = %#v, %v with %d commands, want nothing", panes, err, len(calls))
	}
}

func TestListPanesReportsBrokenSessionsAndKeepsOthers(t *testing.T) {
	t.Parallel()
	socketDir, infoDir := t.TempDir(), t.TempDir()
	for _, name := range []string{"failing", "garbled", "good"} {
		startSessionServer(t, socketDir, name, testNow.Add(-time.Hour))
	}
	var calls []scopedCall
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
		Run:       scopedRunner(&calls, map[string]string{"good": paneListJSON, "garbled": ""}, map[string]error{"failing": errSessionBroken}),
		SocketDir: socketDir, SessionInfoDir: infoDir, Now: func() time.Time { return testNow },
	})
	if !errors.Is(err, errSessionBroken) || !strings.Contains(err.Error(), `"failing"`) || !strings.Contains(err.Error(), `"garbled"`) {
		t.Fatalf("err = %v, want it to name both broken sessions", err)
	}
	if got := sessionNames(panes); !reflect.DeepEqual(got, []string{"good"}) {
		t.Fatalf("sessions = %v, want the healthy session", got)
	}
}

func TestListPanesSkipsWhenZellijIsNotInstalled(t *testing.T) {
	t.Parallel()
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{LookPath: func(string) (string, error) {
		return "", errBinaryNotFound
	}})
	if err != nil || panes != nil {
		t.Fatalf("ListPanesWithOptions() = %#v, %v", panes, err)
	}
}

func TestListPanesFindsNothingWithoutSocketDir(t *testing.T) {
	t.Parallel()
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
		Run:       scopedRunner(new([]scopedCall), nil, nil),
		SocketDir: filepath.Join(t.TempDir(), "missing"), SessionInfoDir: t.TempDir(), Now: func() time.Time { return testNow },
	})
	if err != nil || len(panes) != 0 {
		t.Fatalf("ListPanesWithOptions() = %#v, %v", panes, err)
	}
}

func TestListPanesReadsSocketDirFromEnvironment(t *testing.T) {
	socketDir := t.TempDir()
	t.Setenv("ZELLIJ_SOCKET_DIR", socketDir)
	startSessionServer(t, socketDir, "work", time.Now().Add(-time.Hour))
	var calls []scopedCall
	panes, err := zellij.ListPanesWithOptions(context.Background(), zellij.ListOptions{
		Run:            scopedRunner(&calls, map[string]string{"work": paneListJSON}, nil),
		SessionInfoDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionNames(panes); !reflect.DeepEqual(got, []string{"work"}) {
		t.Fatalf("sessions = %v", got)
	}
}
