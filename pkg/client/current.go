package client

import (
	"context"
	"fmt"
	"os"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
	"github.com/zigai/aht/v2/pkg/zellij"
)

// CurrentContextOptions identifies the process whose enclosing agent session is requested.
// A zero PID uses the calling process.
type CurrentContextOptions struct{ PID int }

type currentInspectors struct {
	PID           int
	ProcessList   func(context.Context) ([]processinfo.Process, error)
	ProcessFind   func(context.Context, int) (processinfo.Process, bool, error)
	TmuxCurrent   func(context.Context) (registry.Location, error)
	ZellijCurrent func() registry.Location
	HerdrCurrent  func() registry.Location
}

// Current resolves the session for the calling agent context.
func (c *Client) Current(ctx context.Context) (registry.Session, error) {
	return c.CurrentWithOptions(ctx, CurrentContextOptions{PID: 0})
}

// CurrentWithOptions resolves an agent using process ancestry and verified terminal evidence.
func (c *Client) CurrentWithOptions(ctx context.Context, opts CurrentContextOptions) (registry.Session, error) {
	return c.currentWithInspectors(ctx, currentInspectors{PID: opts.PID, ProcessList: nil, ProcessFind: nil, TmuxCurrent: nil, ZellijCurrent: nil, HerdrCurrent: nil})
}

func (c *Client) currentWithInspectors(ctx context.Context, opts currentInspectors) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}
	if err := ctx.Err(); err != nil {
		return registry.Session{}, fmt.Errorf("current session: %w", err)
	}
	opts = normalizeCurrentOptions(opts)

	sessions, err := c.store.List(ctx, registry.Filter{
		Harness:            "",
		Presence:           registry.PresenceLive,
		Activity:           "",
		MultiplexerSession: "",
		Project:            "",
		ProjectSubtree:     false,
		CWD:                "",
		MultiplexerKind:    "",
		MultiplexerServer:  "",
		MultiplexerPane:    "",
	})
	if err != nil {
		return registry.Session{}, publicError(err)
	}
	if len(sessions) == 0 {
		return registry.Session{}, ErrNoCurrentSession
	}

	// 1. Check process tree ancestors.
	if session, found, err := resolveCurrentFromAncestors(ctx, sessions, opts); err != nil {
		return registry.Session{}, err
	} else if found {
		return session, nil
	}

	// 2. Check verified terminal context.
	if session, found, err := resolveCurrentFromTerminal(ctx, sessions, opts); err != nil {
		return registry.Session{}, err
	} else if found {
		return session, nil
	}

	if err := ctx.Err(); err != nil {
		return registry.Session{}, fmt.Errorf("current session: %w", err)
	}
	return registry.Session{}, ErrNoCurrentSession
}

func normalizeCurrentOptions(opts currentInspectors) currentInspectors {
	if opts.PID <= 0 {
		opts.PID = os.Getpid()
	}
	if opts.ProcessList == nil {
		opts.ProcessList = processinfo.List
	}
	if opts.ProcessFind == nil {
		opts.ProcessFind = processinfo.Find
	}
	if opts.TmuxCurrent == nil {
		opts.TmuxCurrent = tmux.Current
	}
	if opts.ZellijCurrent == nil {
		opts.ZellijCurrent = zellij.Current
	}
	if opts.HerdrCurrent == nil {
		opts.HerdrCurrent = herdr.Current
	}
	return opts
}

func resolveCurrentFromAncestors(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var zero registry.Session
	var byPID map[int]processinfo.Process
	if opts.ProcessList != nil {
		if procs, err := opts.ProcessList(ctx); err == nil && len(procs) > 0 {
			byPID = buildProcessIndex(procs)
		}
	}
	sessionsByPID := indexSessionsByPID(sessions)

	currPID := opts.PID
	seen := make(map[int]bool)
	for currPID > 0 && !seen[currPID] {
		seen[currPID] = true
		proc, ok := lookupProcess(ctx, currPID, byPID, opts.ProcessFind)
		if !ok {
			break
		}

		if candidates, found := sessionsByPID[currPID]; found {
			session, matched, matchErr := matchAncestorSession(candidates, proc, currPID)
			if matchErr != nil {
				return zero, false, matchErr
			}
			if matched {
				return session, true, nil
			}
		}

		currPID = proc.PPID
	}

	return zero, false, nil
}

func buildProcessIndex(procs []processinfo.Process) map[int]processinfo.Process {
	byPID := make(map[int]processinfo.Process, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	return byPID
}

func indexSessionsByPID(sessions []registry.Session) map[int][]registry.Session {
	sessionsByPID := make(map[int][]registry.Session)
	for _, s := range sessions {
		if s.Process != nil && s.Process.Complete() {
			sessionsByPID[s.Process.PID] = append(sessionsByPID[s.Process.PID], s)
		}
	}
	return sessionsByPID
}

func lookupProcess(
	ctx context.Context,
	pid int,
	byPID map[int]processinfo.Process,
	find func(context.Context, int) (processinfo.Process, bool, error),
) (processinfo.Process, bool) {
	var zero processinfo.Process
	if proc, ok := byPID[pid]; ok {
		return proc, true
	}
	if find != nil {
		if foundProc, found, _ := find(ctx, pid); found {
			return foundProc, true
		}
	}
	return zero, false
}

func matchAncestorSession(
	candidates []registry.Session,
	proc processinfo.Process,
	currPID int,
) (registry.Session, bool, error) {
	var zero registry.Session
	var matched []registry.Session
	for _, cand := range candidates {
		if cand.Process.StartIdentity == proc.StartIdentity {
			matched = append(matched, cand)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], true, nil
	case 0:
		return zero, false, nil
	default:
		return zero, false, fmt.Errorf("%w: multiple sessions matched process %d", ErrAmbiguousSession, currPID)
	}
}

func resolveCurrentFromTerminal(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	if s, found, err := resolveCurrentTmux(ctx, sessions, opts); err != nil || found {
		return s, found, err
	}
	if s, found, err := resolveCurrentZellij(ctx, sessions, opts); err != nil || found {
		return s, found, err
	}
	if s, found, err := resolveCurrentHerdr(ctx, sessions, opts); err != nil || found {
		return s, found, err
	}
	var zero registry.Session
	return zero, false, nil
}

func resolveCurrentTmux(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var zero registry.Session
	tmuxCtx, err := opts.TmuxCurrent(ctx)
	if err != nil {
		return zero, false, nil //nolint:nilerr // tmux discovery errors fall back to other multiplexer detection
	}
	if tmuxCtx.Empty() || tmuxCtx.PaneID == "" || tmuxCtx.ServerID == "" || tmuxCtx.PaneTTY == "" {
		return zero, false, nil
	}
	return resolvePaneSession(ctx, sessions, registry.MultiplexerTmux, tmuxCtx.ServerID, tmuxCtx.PaneID, "", opts)
}

func resolveCurrentZellij(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var zero registry.Session
	zCtx := opts.ZellijCurrent()
	if zCtx.Empty() || zCtx.PaneID == "" || zCtx.SessionName == "" {
		return zero, false, nil
	}
	return resolvePaneSession(ctx, sessions, registry.MultiplexerZellij, "", zCtx.PaneID, zCtx.SessionName, opts)
}

func resolveCurrentHerdr(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var zero registry.Session
	hCtx := opts.HerdrCurrent()
	if hCtx.Empty() || hCtx.PaneID == "" {
		return zero, false, nil
	}
	return resolvePaneSession(ctx, sessions, registry.MultiplexerHerdr, hCtx.ServerID, hCtx.PaneID, hCtx.SessionName, opts)
}

func matchesPaneLocation(
	s registry.Session,
	kind registry.MultiplexerKind,
	serverID string,
	paneID string,
	sessionName string,
) bool {
	if s.Location.Kind != kind {
		return false
	}
	if s.Location.PaneID != paneID {
		return false
	}
	if serverID != "" && !registry.MatchesServer(s, serverID) {
		return false
	}
	if sessionName != "" && s.Location.SessionName != sessionName {
		return false
	}
	return true
}

func verifyCandidateProcess(
	ctx context.Context,
	cand registry.Session,
	opts currentInspectors,
) bool {
	find := opts.ProcessFind
	if cand.Process == nil || !cand.Process.Complete() || find == nil {
		return false
	}
	proc, found, err := find(ctx, cand.Process.PID)
	if err != nil || !found || proc.StartIdentity != cand.Process.StartIdentity {
		return false
	}
	caller, found, err := find(ctx, opts.PID)
	return err == nil && found && caller.TTY != "" && caller.TTY == proc.TTY
}

func resolvePaneSession(
	ctx context.Context,
	sessions []registry.Session,
	kind registry.MultiplexerKind,
	serverID string,
	paneID string,
	sessionName string,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var zero registry.Session
	var paneMatches []registry.Session
	for _, s := range sessions {
		if matchesPaneLocation(s, kind, serverID, paneID, sessionName) && verifyCandidateProcess(ctx, s, opts) {
			paneMatches = append(paneMatches, s)
		}
	}

	switch len(paneMatches) {
	case 0:
		return zero, false, nil
	case 1:
		return paneMatches[0], true, nil
	default:
		return zero, false, fmt.Errorf("%w: multiple live sessions in pane %s", ErrAmbiguousSession, paneID)
	}
}
