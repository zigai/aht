package client

import (
	"context"
	"fmt"
	"os"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

// CurrentOptions identifies the process whose enclosing agent session is requested.
// A zero PID uses the calling process.
type CurrentOptions struct{ PID int }

type currentInspectors struct {
	PID         int
	ProcessList func(context.Context) ([]processinfo.Process, error)
	ProcessFind func(context.Context, int) (processinfo.Process, bool, error)
	Locations   func(context.Context) []registry.Location
}

// Current resolves the session for the calling agent context.
func (c *Client) Current(ctx context.Context) (registry.Session, error) {
	return c.CurrentWithOptions(ctx, CurrentOptions{PID: 0})
}

// CurrentWithOptions resolves an agent using process ancestry and verified terminal evidence.
func (c *Client) CurrentWithOptions(
	ctx context.Context,
	opts CurrentOptions,
) (registry.Session, error) {
	session, err := c.currentWithInspectors(
		ctx,
		currentInspectors{
			PID:         opts.PID,
			ProcessList: nil,
			ProcessFind: nil,
			Locations:   nil,
		},
	)
	if err != nil {
		return session, fmt.Errorf("current session: %w", err)
	}

	return session, nil
}

//nolint:wrapcheck // CurrentWithOptions adds operation context at the public boundary.
func (c *Client) currentWithInspectors(
	ctx context.Context,
	opts currentInspectors,
) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}

	if err := ctx.Err(); err != nil {
		return registry.Session{}, err
	}

	opts = normalizeCurrentOptions(opts)

	sessions, err := c.store.List(ctx, registry.Filter{
		Presence: registry.PresenceLive,
	})
	if err != nil {
		return registry.Session{}, err
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
		return registry.Session{}, err
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

	if opts.Locations == nil {
		opts.Locations = currentLocations
	}

	return opts
}

func resolveCurrentFromAncestors(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var (
		zero  registry.Session
		byPID map[int]processinfo.Process
	)

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
	var (
		zero    registry.Session
		matched []registry.Session
	)

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
		return zero, false, fmt.Errorf(
			"%w: multiple sessions matched process %d",
			ErrAmbiguousSession,
			currPID,
		)
	}
}

func currentLocations(ctx context.Context) []registry.Location {
	var locations []registry.Location

	for _, driver := range drivers.All() {
		location, err := driver.Current(ctx)
		if err == nil && !location.Empty() {
			locations = append(locations, location)
		}
	}

	return locations
}

func resolveCurrentFromTerminal(
	ctx context.Context,
	sessions []registry.Session,
	opts currentInspectors,
) (registry.Session, bool, error) {
	for _, location := range opts.Locations(ctx) {
		driver, ok := drivers.Find(location.Kind)
		if !ok {
			continue
		}

		if s, found, err := resolvePaneSession(
			ctx,
			sessions,
			driver,
			location,
			opts,
		); err != nil ||
			found {
			return s, found, err
		}
	}

	var zero registry.Session

	return zero, false, nil
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
	driver mux.Driver,
	location registry.Location,
	opts currentInspectors,
) (registry.Session, bool, error) {
	var (
		zero        registry.Session
		paneMatches []registry.Session
	)

	for _, s := range sessions {
		if driver.SamePane(s.Location, location) &&
			verifyCandidateProcess(ctx, s, opts) {
			paneMatches = append(paneMatches, s)
		}
	}

	switch len(paneMatches) {
	case 0:
		return zero, false, nil
	case 1:
		return paneMatches[0], true, nil
	default:
		return zero, false, fmt.Errorf(
			"%w: multiple live sessions in pane %s",
			ErrAmbiguousSession,
			location.PaneID,
		)
	}
}
