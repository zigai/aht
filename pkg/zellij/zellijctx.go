package zellij

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/command"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

const defaultCaptureLines = 100

var errPaneRequired = errors.New("zellij session and pane are required")

type Env struct {
	SessionName string
	PaneID      string
}

type CommandRunner func(context.Context, ...string) (string, error)

type ListOptions struct {
	Run      CommandRunner
	LookPath func(string) (string, error)
}

type CaptureOptions struct {
	Run CommandRunner
}

type paneRecord struct {
	ID          uint32 `json:"id"`
	IsPlugin    bool   `json:"is_plugin"`
	Title       string `json:"title"`
	Exited      bool   `json:"exited"`
	TabID       int    `json:"tab_id"`
	TabPosition int    `json:"tab_position"`
	TabName     string `json:"tab_name"`
	PaneCommand string `json:"pane_command"`
	PaneCWD     string `json:"pane_cwd"`
}

func Current() registry.MultiplexerContext {
	return CurrentWithEnv(Env{SessionName: os.Getenv("ZELLIJ_SESSION_NAME"), PaneID: os.Getenv("ZELLIJ_PANE_ID")})
}

func CurrentWithEnv(env Env) registry.MultiplexerContext {
	if strings.TrimSpace(env.SessionName) == "" || strings.TrimSpace(env.PaneID) == "" {
		var empty registry.MultiplexerContext
		return empty
	}
	return registry.MultiplexerContext{ //nolint:exhaustruct_v5 // env exposes only session and pane
		Kind: registry.MultiplexerZellij, SessionName: env.SessionName, PaneID: normalizePaneID(env.PaneID),
	}
}

func ListPanes(ctx context.Context) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, ListOptions{Run: nil, LookPath: nil})
}

func ListPanesWithOptions(ctx context.Context, options ListOptions) ([]mux.Pane, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list zellij panes: %w", err)
	}
	lookPath := options.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if options.Run == nil {
		if _, err := lookPath("zellij"); err != nil {
			//nolint:nilerr // an optional unavailable multiplexer contributes no panes
			return nil, nil
		}
		options.Run = runZellij
	}
	sessionOutput, err := options.Run(ctx, "list-sessions", "--no-formatting")
	if err != nil {
		if strings.Contains(strings.ToLower(sessionOutput), "no active zellij sessions") {
			return nil, nil
		}
		return nil, fmt.Errorf("list zellij sessions: %w", err)
	}
	sessions := parseSessions(sessionOutput)
	panes := make([]mux.Pane, 0)
	var listErrors []error
	for _, session := range sessions {
		output, listErr := options.Run(ctx, "--session", session, "action", "list-panes", "--all", "--json")
		if listErr != nil {
			listErrors = append(listErrors, fmt.Errorf("list zellij session %q panes: %w", session, listErr))
			continue
		}
		parsed, parseErr := parsePanes(session, output)
		if parseErr != nil {
			return nil, parseErr
		}
		panes = append(panes, parsed...)
	}
	return panes, errors.Join(listErrors...)
}

func CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePaneWithOptions(ctx, pane, CaptureOptions{Run: nil})
}

func CapturePaneWithOptions(ctx context.Context, pane mux.Pane, options CaptureOptions) (mux.ScreenSnapshot, error) {
	if pane.Location.Kind != registry.MultiplexerZellij || pane.Location.SessionName == "" || pane.Location.PaneID == "" {
		return mux.ScreenSnapshot{}, errPaneRequired
	}
	run := options.Run
	if run == nil {
		run = runZellij
	}
	text, err := run(ctx, "--session", pane.Location.SessionName, "action", "dump-screen", "--pane-id", pane.Location.PaneID)
	if err != nil {
		return mux.ScreenSnapshot{}, fmt.Errorf("capture zellij pane: %w", err)
	}
	return mux.ScreenSnapshot{Text: strings.Join(mux.BoundBottomLines(text, defaultCaptureLines), "\n"), Title: pane.Title}, nil
}

func parseSessions(output string) []string {
	var sessions []string
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "(EXITED") {
			continue
		}
		if name, _, ok := strings.Cut(line, " [Created "); ok {
			line = strings.TrimSpace(name)
		}
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions
}

func parsePanes(session string, output string) ([]mux.Pane, error) {
	var records []paneRecord
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		return nil, fmt.Errorf("parse zellij panes: %w", err)
	}
	panes := make([]mux.Pane, 0, len(records))
	for _, record := range records {
		if record.IsPlugin || record.Exited {
			continue
		}
		paneID := "terminal_" + strconv.FormatUint(uint64(record.ID), 10)
		location := registry.MultiplexerContext{ //nolint:exhaustruct_v5 // pane JSON lacks process and TTY
			Kind: registry.MultiplexerZellij, SessionName: session,
			TabID: strconv.Itoa(record.TabID), TabIndex: strconv.Itoa(record.TabPosition), TabName: record.TabName,
			PaneID: paneID, PaneCurrentPath: record.PaneCWD,
		}
		panes = append(panes, mux.Pane{ //nolint:exhaustruct_v5 // CLI lacks process references and activity
			Location: location, Command: record.PaneCommand, CWD: record.PaneCWD, Title: record.Title,
		})
	}
	return panes, nil
}

func normalizePaneID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "terminal_") || strings.HasPrefix(value, "plugin_") {
		return value
	}
	return "terminal_" + value
}

func runZellij(ctx context.Context, args ...string) (string, error) {
	output, err := command.Run(ctx, "zellij", nil, args...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return string(output), fmt.Errorf("run zellij command: %w", ctxErr)
		}
		return string(output), fmt.Errorf("run zellij command: %w", err)
	}
	return string(output), nil
}
