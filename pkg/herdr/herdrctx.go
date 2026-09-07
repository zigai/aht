package herdr

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zigai/aht/internal/command"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

var (
	errPaneRequired          = errors.New("herdr pane is required")
	errInvalidSessionsOutput = errors.New("invalid herdr sessions output")
)

type Env struct {
	Enabled     string
	SessionName string
	SocketPath  string
	WorkspaceID string
	TabID       string
	PaneID      string
}

type CommandRunner func(context.Context, map[string]string, ...string) (string, error)

type ListOptions struct {
	Run      CommandRunner
	LookPath func(string) (string, error)
}

type CaptureOptions struct {
	Run CommandRunner
}

type herdrNamedItem struct {
	ID    string `json:"id"`
	WsID  string `json:"workspace_id"`
	TabID string `json:"tab_id"`
	Name  string `json:"name"`
	Label string `json:"label"`
	Title string `json:"title"`
}

type herdrPaneItem struct {
	PaneID        string `json:"pane_id"`
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	TabID         string `json:"tab_id"`
	ForegroundCWD string `json:"foreground_cwd"`
	CWD           string `json:"cwd"`
	Title         string `json:"terminal_title_stripped"`
	PaneTitle     string `json:"title"`
	Label         string `json:"label"`
	AgentStatus   string `json:"agent_status"`
	Status        string `json:"status"`
	AgentName     string `json:"agent_name"`
	Agent         string `json:"agent"`
	Exited        bool   `json:"exited"`
	Closed        bool   `json:"closed"`
}

type herdrAgentItem struct {
	PaneID      string `json:"pane_id"`
	Agent       string `json:"agent"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	AgentStatus string `json:"agent_status"`
	Status      string `json:"status"`
	State       string `json:"state"`
}

type herdrSnapshotData struct {
	Workspaces []herdrNamedItem `json:"workspaces"`
	Tabs       []herdrNamedItem `json:"tabs"`
	Panes      []herdrPaneItem  `json:"panes"`
	Agents     []herdrAgentItem `json:"agents"`
}

type herdrProcessItem struct {
	PID     int      `json:"pid"`
	Cmdline string   `json:"cmdline"`
	Command string   `json:"command"`
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
}

type herdrProcessInfoData struct {
	ShellPID            int                `json:"shell_pid"`
	ProcessGroupID      int                `json:"foreground_process_group_id"`
	ForegroundPgid      int                `json:"foreground_pgid"`
	ForegroundProcesses []herdrProcessItem `json:"foreground_processes"`
}

type herdrSessionItem struct {
	Name        string `json:"name"`
	SessionName string `json:"session_name"`
	ID          string `json:"id"`
	Running     *bool  `json:"running"`
	Exited      bool   `json:"exited"`
	Dead        bool   `json:"dead"`
}

func Current() registry.MultiplexerContext {
	return CurrentWithEnv(Env{
		Enabled: os.Getenv("HERDR_ENV"), SessionName: os.Getenv("HERDR_SESSION"), SocketPath: os.Getenv("HERDR_SOCKET_PATH"),
		WorkspaceID: os.Getenv("HERDR_WORKSPACE_ID"), TabID: os.Getenv("HERDR_TAB_ID"), PaneID: os.Getenv("HERDR_PANE_ID"),
	})
}

func CurrentWithEnv(env Env) registry.MultiplexerContext {
	if strings.TrimSpace(env.PaneID) == "" || (env.Enabled != "1" && strings.TrimSpace(env.SocketPath) == "") {
		var empty registry.MultiplexerContext
		return empty
	}
	return registry.MultiplexerContext{ //nolint:exhaustruct_v5 // env exposes only Herdr identity
		Kind: registry.MultiplexerHerdr, ServerID: env.SocketPath, SessionName: env.SessionName,
		WorkspaceID: env.WorkspaceID, TabID: env.TabID, PaneID: env.PaneID,
	}
}

func ListPanes(ctx context.Context) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, ListOptions{Run: nil, LookPath: nil})
}

//nolint:gocognit,cyclop // native session, snapshot, and process responses are intentionally joined here
func ListPanesWithOptions(ctx context.Context, options ListOptions) ([]mux.Pane, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list herdr panes: %w", err)
	}
	lookPath := options.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if options.Run == nil {
		if _, err := lookPath("herdr"); err != nil {
			//nolint:nilerr // an optional unavailable multiplexer contributes no panes
			return nil, nil
		}
		options.Run = runHerdr
	}
	sessionOutput, err := options.Run(ctx, nil, "session", "list", "--json")
	if err != nil {
		if herdrUnavailable(sessionOutput) {
			return nil, nil
		}
		return nil, fmt.Errorf("list herdr sessions: %w", err)
	}
	sessions, err := parseSessions(sessionOutput)
	if err != nil {
		return nil, err
	}
	panes := make([]mux.Pane, 0)
	var listErrors []error
	for _, session := range sessions {
		env := map[string]string{"HERDR_SESSION": session}
		output, snapshotErr := options.Run(ctx, env, "api", "snapshot")
		if snapshotErr != nil {
			listErrors = append(listErrors, fmt.Errorf("read herdr session %q snapshot: %w", session, snapshotErr))
			continue
		}
		sessionPanes, parseErr := parseSnapshot(session, output)
		if parseErr != nil {
			return nil, parseErr
		}
		for index := range sessionPanes {
			processOutput, processErr := options.Run(ctx, env, "pane", "process-info", "--pane", sessionPanes[index].Location.PaneID)
			if processErr != nil {
				listErrors = append(listErrors, fmt.Errorf("read herdr pane process info: %w", processErr))
				continue
			}
			refs, processGroupID, parseProcessErr := parseProcessInfo(processOutput)
			if parseProcessErr != nil {
				return nil, parseProcessErr
			}
			sessionPanes[index].Processes = refs
			if sessionPanes[index].Location.PanePID == 0 && len(refs) > 0 {
				sessionPanes[index].Location.PanePID = refs[0].PID
			}
			for refIndex := range sessionPanes[index].Processes {
				if sessionPanes[index].Processes[refIndex].ProcessGroupID == 0 {
					sessionPanes[index].Processes[refIndex].ProcessGroupID = processGroupID
				}
			}
		}
		panes = append(panes, sessionPanes...)
	}
	return panes, errors.Join(listErrors...)
}

func CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePaneWithOptions(ctx, pane, CaptureOptions{Run: nil})
}

func CapturePaneWithOptions(ctx context.Context, pane mux.Pane, options CaptureOptions) (mux.ScreenSnapshot, error) {
	if pane.Location.Kind != registry.MultiplexerHerdr || pane.Location.PaneID == "" {
		return mux.ScreenSnapshot{}, errPaneRequired
	}
	run := options.Run
	if run == nil {
		run = runHerdr
	}
	env := map[string]string{}
	if pane.Location.SessionName != "" {
		env["HERDR_SESSION"] = pane.Location.SessionName
	}
	if pane.Location.ServerID != "" {
		env["HERDR_SOCKET_PATH"] = pane.Location.ServerID
	}
	output, err := run(ctx, env, "pane", "read", pane.Location.PaneID, "--source", "detection")
	if err != nil {
		return mux.ScreenSnapshot{}, fmt.Errorf("capture herdr pane: %w", err)
	}
	return mux.ScreenSnapshot{Text: parseReadOutput(output), Title: pane.Title}, nil
}

func (n herdrNamedItem) Key() string {
	return cmp.Or(n.WsID, n.TabID, n.ID)
}

func (n herdrNamedItem) DisplayName() string {
	return cmp.Or(n.Label, n.Name, n.Title)
}

func parseSessions(output string) ([]string, error) {
	if sessions, ok := parseContainerSessions(output); ok {
		return sessions, nil
	}
	if sessions, ok := parseListSessions(output); ok {
		return sessions, nil
	}
	return nil, fmt.Errorf("parse herdr sessions: %w", errInvalidSessionsOutput)
}

func parseContainerSessions(output string) ([]string, bool) {
	var container struct {
		Result *struct {
			Sessions []herdrSessionItem `json:"sessions"`
		} `json:"result"`
		Sessions []herdrSessionItem `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(output), &container); err != nil {
		return nil, false
	}
	list := container.Sessions
	if container.Result != nil {
		list = container.Result.Sessions
	}
	if len(list) == 0 {
		return nil, false
	}
	var sessions []string
	for _, s := range list {
		if s.Exited || s.Dead || (s.Running != nil && !*s.Running) {
			continue
		}
		name := cmp.Or(s.Name, s.SessionName, s.ID)
		if name != "" {
			sessions = append(sessions, name)
		}
	}
	return sessions, true
}

func parseListSessions(output string) ([]string, bool) {
	var rawList []json.RawMessage
	if err := json.Unmarshal([]byte(output), &rawList); err != nil {
		return nil, false
	}
	var sessions []string
	for _, item := range rawList {
		var str string
		if err := json.Unmarshal(item, &str); err == nil && strings.TrimSpace(str) != "" {
			sessions = append(sessions, strings.TrimSpace(str))
			continue
		}
		var obj herdrSessionItem
		if err := json.Unmarshal(item, &obj); err == nil {
			if obj.Exited || obj.Dead || (obj.Running != nil && !*obj.Running) {
				continue
			}
			name := cmp.Or(obj.Name, obj.SessionName, obj.ID)
			if name != "" {
				sessions = append(sessions, name)
			}
		}
	}
	return sessions, true
}

//nolint:cyclop // pane location and metadata mapping evaluates multiple herdr attributes
func parseSnapshot(session string, output string) ([]mux.Pane, error) {
	var resp struct {
		herdrSnapshotData

		Result struct {
			herdrSnapshotData

			Snapshot *herdrSnapshotData `json:"snapshot"`
		} `json:"result"`
		Snapshot *herdrSnapshotData `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("parse herdr snapshot: %w", err)
	}
	data := resp.herdrSnapshotData
	switch {
	case resp.Snapshot != nil:
		data = *resp.Snapshot
	case resp.Result.Snapshot != nil:
		data = *resp.Result.Snapshot
	case len(resp.Result.Panes) > 0 || len(resp.Result.Workspaces) > 0:
		data = resp.Result.herdrSnapshotData
	}
	agentsByPane := make(map[string]herdrAgentItem, len(data.Agents))
	for _, agent := range data.Agents {
		if agent.PaneID != "" {
			agentsByPane[agent.PaneID] = agent
		}
	}

	workspaceNames := make(map[string]string, len(data.Workspaces))
	for _, ws := range data.Workspaces {
		if key := ws.Key(); key != "" {
			workspaceNames[key] = ws.DisplayName()
		}
	}

	tabNames := make(map[string]string, len(data.Tabs))
	for _, tab := range data.Tabs {
		if key := tab.Key(); key != "" {
			tabNames[key] = tab.DisplayName()
		}
	}

	panes := make([]mux.Pane, 0, len(data.Panes))
	for _, item := range data.Panes {
		if item.Exited || item.Closed {
			continue
		}
		paneID := cmp.Or(item.PaneID, item.ID)
		if paneID == "" {
			continue
		}
		agent := agentsByPane[paneID]
		status := cmp.Or(item.AgentStatus, item.Status, agent.AgentStatus, agent.Status, agent.State)
		activity := herdrActivity(status)
		cwd := cmp.Or(item.ForegroundCWD, item.CWD)
		label := cmp.Or(agent.Agent, agent.Name, agent.Label, item.Agent, item.AgentName)
		title := cmp.Or(item.Title, item.PaneTitle, item.Label, label)

		location := registry.MultiplexerContext{ //nolint:exhaustruct_v5 // snapshot lacks server, window, TTY, and process
			Kind:            registry.MultiplexerHerdr,
			SessionName:     session,
			WorkspaceID:     item.WorkspaceID,
			WorkspaceName:   workspaceNames[item.WorkspaceID],
			TabID:           item.TabID,
			TabName:         tabNames[item.TabID],
			PaneID:          paneID,
			PaneCurrentPath: cwd,
		}
		panes = append(panes, mux.Pane{ //nolint:exhaustruct_v5 // process references populated below
			Location:    location,
			CWD:         cwd,
			Command:     label,
			Title:       title,
			Activity:    activity,
			StateReason: "herdr_agent_status",
		})
	}
	return panes, nil
}

func parseProcessInfo(output string) ([]mux.ProcessRef, int, error) {
	var resp struct {
		herdrProcessInfoData

		Result struct {
			herdrProcessInfoData

			ProcessInfo *herdrProcessInfoData `json:"process_info"`
		} `json:"result"`
		ProcessInfo *herdrProcessInfoData `json:"process_info"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, 0, fmt.Errorf("parse herdr process info: %w", err)
	}
	data := resp.herdrProcessInfoData
	switch {
	case resp.ProcessInfo != nil:
		data = *resp.ProcessInfo
	case resp.Result.ProcessInfo != nil:
		data = *resp.Result.ProcessInfo
	case len(resp.Result.ForegroundProcesses) > 0 || resp.Result.ShellPID > 0:
		data = resp.Result.herdrProcessInfoData
	}
	processGroupID := cmp.Or(data.ProcessGroupID, data.ForegroundPgid)
	refs := make([]mux.ProcessRef, 0, len(data.ForegroundProcesses)+1)
	for _, item := range data.ForegroundProcesses {
		if item.PID <= 0 {
			continue
		}
		command := cmp.Or(item.Cmdline, item.Command, item.Name)
		if command == "" {
			argv := item.Argv
			if len(argv) == 0 {
				argv = item.Args
			}
			if len(argv) > 0 {
				command = strings.Join(argv, " ")
			}
		}
		refs = append(refs, mux.ProcessRef{
			PID:            item.PID,
			ProcessGroupID: processGroupID,
			Command:        command,
			CWD:            item.CWD,
		})
	}
	if data.ShellPID > 0 {
		refs = append(refs, mux.ProcessRef{PID: data.ShellPID, ProcessGroupID: 0, Command: "", CWD: ""})
	}
	return refs, processGroupID, nil
}

func parseReadOutput(output string) string {
	var resp struct {
		Result struct {
			Text    string `json:"text"`
			Content string `json:"content"`
			Output  string `json:"output"`
		} `json:"result"`
		Text    string `json:"text"`
		Content string `json:"content"`
		Output  string `json:"output"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err == nil {
		if text := cmp.Or(resp.Result.Text, resp.Result.Content, resp.Result.Output, resp.Text, resp.Content, resp.Output); text != "" {
			return text
		}
	}
	return output
}

func herdrActivity(status string) *registry.Activity {
	var activity registry.Activity
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "running":
		activity = registry.ActivityRunning
	case "blocked", "waiting":
		activity = registry.ActivityWaiting
	case "idle", "done":
		activity = registry.ActivityIdle
	case "unknown", "":
		return nil
	default:
		return nil
	}
	return &activity
}

func herdrUnavailable(output string) bool {
	value := strings.ToLower(output)
	return strings.Contains(value, "not running") || strings.Contains(value, "no session") || strings.Contains(value, "connection refused")
}

func runHerdr(ctx context.Context, env map[string]string, args ...string) (string, error) {
	commandEnv := make([]string, 0, len(os.Environ())+len(env))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := env[key]; !overridden {
			commandEnv = append(commandEnv, entry)
		}
	}
	for key, value := range env {
		commandEnv = append(commandEnv, key+"="+value)
	}
	output, err := command.Run(ctx, "herdr", commandEnv, args...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return string(output), fmt.Errorf("run herdr command: %w", ctxErr)
		}
		return string(output), fmt.Errorf("run herdr command: %w", err)
	}
	return string(output), nil
}
