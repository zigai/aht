package herdr_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	errUnexpectedCommand = errors.New("unexpected herdr command")
	errBinaryNotFound    = errors.New("binary not found")
)

func TestCurrentWithEnvUsesManagedPaneIdentity(t *testing.T) {
	t.Parallel()
	context := herdr.CurrentWithEnv(herdr.Env{Enabled: "1", SessionName: "work", SocketPath: "/tmp/herdr.sock", WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"})
	if context.Kind != registry.MultiplexerHerdr || context.ServerID != "/tmp/herdr.sock" || context.SessionName != "work" || context.WorkspaceID != "w1" || context.TabID != "w1:t1" || context.PaneID != "w1:p1" {
		t.Fatalf("CurrentWithEnv() = %#v", context)
	}
	if got := herdr.CurrentWithEnv(herdr.Env{PaneID: "w1:p1"}); !got.Empty() {
		t.Fatalf("unmanaged environment produced context: %#v", got)
	}
}

//nolint:cyclop // fixture runner validates the exact native command sequence and response mapping
func TestListPanesUsesSnapshotProcessInfoAndSemanticState(t *testing.T) {
	t.Parallel()
	type call struct {
		env  map[string]string
		args []string
	}
	var calls []call
	run := func(_ context.Context, env map[string]string, args ...string) (string, error) {
		calls = append(calls, call{env: env, args: append([]string(nil), args...)})
		switch strings.Join(args, " ") {
		case "session list --json":
			return `{"sessions":[{"name":"work"}]}`, nil
		case "api snapshot":
			return `{"result":{"workspaces":[{"workspace_id":"w1","label":"repo"}],"tabs":[{"tab_id":"w1:t1","label":"agents"}],"panes":[{"pane_id":"w1:p1","workspace_id":"w1","tab_id":"w1:t1","foreground_cwd":"/repo","terminal_title_stripped":"Codex"}],"agents":[{"pane_id":"w1:p1","agent":"codex","agent_status":"blocked"}]}}`, nil
		case "pane process-info --pane w1:p1":
			return `{"result":{"shell_pid":40,"foreground_process_group_id":42,"foreground_processes":[{"pid":42,"name":"codex","argv":["codex"],"cwd":"/repo"}]}}`, nil
		default:
			return "", errUnexpectedCommand
		}
	}
	panes, err := herdr.ListPanesWithOptions(context.Background(), herdr.ListOptions{Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 {
		t.Fatalf("panes = %#v", panes)
	}
	pane := panes[0]
	if pane.Location.Kind != registry.MultiplexerHerdr || pane.Location.SessionName != "work" || pane.Location.WorkspaceName != "repo" || pane.Location.TabName != "agents" || pane.Location.PaneID != "w1:p1" || pane.Location.PanePID != 42 {
		t.Fatalf("pane location = %#v", pane.Location)
	}
	if pane.Activity == nil || *pane.Activity != registry.ActivityWaiting || pane.StateReason != "herdr_agent_status" {
		t.Fatalf("pane state = %#v", pane)
	}
	if len(pane.Processes) != 2 || pane.Processes[0].PID != 42 || pane.Processes[0].ProcessGroupID != 42 || pane.Processes[1].PID != 40 {
		t.Fatalf("process references = %#v", pane.Processes)
	}
	if len(calls) != 3 || calls[1].env["HERDR_SESSION"] != "work" || calls[2].env["HERDR_SESSION"] != "work" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestListPanesSkipsStoppedSessions(t *testing.T) {
	t.Parallel()
	var calls [][]string
	run := func(_ context.Context, _ map[string]string, args ...string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if strings.Join(args, " ") == "session list --json" {
			return `{"sessions":[{"default":true,"name":"default","running":false}]}`, nil
		}
		return "", errUnexpectedCommand
	}

	panes, err := herdr.ListPanesWithOptions(context.Background(), herdr.ListOptions{Run: run})
	if err != nil || len(panes) != 0 {
		t.Fatalf("ListPanesWithOptions() = %#v, %v", panes, err)
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "session list --json" {
		t.Fatalf("stopped session triggered API calls: %#v", calls)
	}
}

func TestListPanesSkipsWhenHerdrIsNotInstalled(t *testing.T) {
	panes, err := herdr.ListPanesWithOptions(context.Background(), herdr.ListOptions{LookPath: func(string) (string, error) {
		return "", errBinaryNotFound
	}})
	if err != nil || panes != nil {
		t.Fatalf("ListPanesWithOptions() = %#v, %v", panes, err)
	}
}

func TestListPanesAcceptsEmptySessionContainer(t *testing.T) {
	t.Parallel()
	panes, err := herdr.ListPanesWithOptions(context.Background(), herdr.ListOptions{Run: func(_ context.Context, _ map[string]string, args ...string) (string, error) {
		if strings.Join(args, " ") != "session list --json" {
			return "", errUnexpectedCommand
		}
		return `{"sessions":[]}`, nil
	}})
	if err != nil || len(panes) != 0 {
		t.Fatalf("ListPanesWithOptions() = %#v, %v", panes, err)
	}
}

func TestCapturePaneUsesDetectionBuffer(t *testing.T) {
	t.Parallel()
	var gotEnv map[string]string
	var gotArgs []string
	snapshot, err := herdr.CapturePaneWithOptions(context.Background(), mux.Pane{
		Location: registry.Location{Kind: registry.MultiplexerHerdr, ServerID: "/tmp/herdr.sock", SessionName: "work", PaneID: "w1:p1"},
		Title:    "Codex",
	}, herdr.CaptureOptions{Run: func(_ context.Context, env map[string]string, args ...string) (string, error) {
		gotEnv = env
		gotArgs = append([]string(nil), args...)
		return `{"result":{"text":"permission prompt"}}`, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"pane", "read", "w1:p1", "--source", "detection"}) || gotEnv["HERDR_SESSION"] != "work" || gotEnv["HERDR_SOCKET_PATH"] != "/tmp/herdr.sock" {
		t.Fatalf("capture call = %#v %#v", gotEnv, gotArgs)
	}
	if snapshot.Text != "permission prompt" || snapshot.Title != "Codex" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
