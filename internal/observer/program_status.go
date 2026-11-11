package observer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	programStatusReason        = "program_status"
	programStatusClearedReason = "program_status_cleared"
)

const (
	statusWatchRunning statusWatchState = iota + 1
	statusWatchUnsignaled
)

type statusServer struct {
	kind registry.MultiplexerKind
	id   string
}

type statusWatchState uint8

type statusWatchers struct {
	watch mux.ProgramStatusWatch
	read  func(context.Context, []mux.Pane)
	state map[statusServer]statusWatchState
	//exhaustruct:optional
	group sync.WaitGroup
	//exhaustruct:optional
	mu sync.Mutex
}

func programStatusReading(
	session registry.Session,
	identity *registry.ProcessIdentity,
	pane mux.Pane,
	at time.Time,
) (registry.Observation, bool, bool) {
	var unchanged registry.Observation

	stored := session.Observations.Screen
	if stored != nil &&
		(stored.Authority != registry.AuthorityTerminal || !stored.Process.Equal(*identity)) {
		stored = nil
	}

	activity, detail, ok := trustedProgramStatus(session.Harness, pane)
	if !ok {
		if stored == nil || stored.Activity == registry.ActivityUnknown {
			return unchanged, false, false
		}

		return terminalReading(
			session.Harness, identity, at,
			registry.ActivityUnknown, "", programStatusClearedReason,
		), true, true
	}

	// An unchanged record is not sent again, so a quiet session writes nothing.
	if stored != nil && stored.Activity == activity && stored.Detail == detail &&
		programStatusApplied(session, activity, at) {
		return unchanged, false, true
	}

	return terminalReading(
		session.Harness, identity, at, activity, detail, programStatusReason,
	), true, true
}

func trustedProgramStatus(
	harnessID registry.Harness,
	pane mux.Pane,
) (registry.Activity, registry.ActivityDetail, bool) {
	// Any program in the pane can write the record, so it must name the harness.
	app := catalog.ProgramStatusApp(harnessID)
	if pane.Program == nil || app == "" || pane.Program.App != app {
		return "", "", false
	}

	return pane.Program.Activity()
}

func programStatusApplied(session registry.Session, activity registry.Activity, at time.Time) bool {
	decision := session.Decision()
	if decision == nil {
		return false
	}

	authority, _ := registry.ActivityAuthority(
		session,
		(catalog.Rules{}).Policy(session.Harness),
		at,
	)
	if decision.Authority != authority {
		return false
	}

	return authority != registry.AuthorityTerminal ||
		session.Activity() != nil && *session.Activity() == activity
}

func terminalReading(
	harnessID registry.Harness,
	identity *registry.ProcessIdentity,
	at time.Time,
	activity registry.Activity,
	detail registry.ActivityDetail,
	reason string,
) registry.Observation {
	var subject registry.ObservationIdentity

	return registry.Observation{
		Harness: harnessID,
		At:      at,
		Subject: subject,
		Evidence: (*registry.Reading)(&registry.ScreenObservation{
			Activity:               activity,
			Detail:                 detail,
			Authority:              registry.AuthorityTerminal,
			Reason:                 reason,
			RuleID:                 "",
			ManifestSource:         "",
			ManifestVersion:        0,
			FallbackForIntegration: "",
			FallbackReason:         "",
			Process:                *identity,
			ObservedAt:             at,
		}),
	}
}

func newStatusWatchers(
	watch mux.ProgramStatusWatch,
	read func(context.Context, []mux.Pane),
) *statusWatchers {
	return &statusWatchers{
		watch: watch,
		read:  read,
		state: make(map[statusServer]statusWatchState),
	}
}

func (w *statusWatchers) sync(ctx context.Context, servers map[statusServer]struct{}) {
	if w.watch == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for server, state := range w.state {
		if _, listed := servers[server]; !listed && state == statusWatchUnsignaled {
			delete(w.state, server)
		}
	}

	for server := range servers {
		if _, known := w.state[server]; known {
			continue
		}

		w.state[server] = statusWatchRunning
		w.group.Go(func() { w.run(ctx, server) })
	}
}

func (w *statusWatchers) run(ctx context.Context, server statusServer) {
	err := w.watch(ctx, server.kind, server.id, func(panes []mux.Pane) { w.read(ctx, panes) })

	w.mu.Lock()
	defer w.mu.Unlock()

	if errors.Is(err, mux.ErrProgramStatusUnsignaled) {
		w.state[server] = statusWatchUnsignaled

		return
	}

	delete(w.state, server)
}

func (o *Observer) readProgramStatus(ctx context.Context, panes []mux.Pane) {
	o.statusMu.Lock()
	defer o.statusMu.Unlock()

	sessions, err := o.store.List(ctx, registry.Filter{})
	if err == nil {
		observations := programStatusObservations(sessions, panes, o.now().UTC())
		if len(observations) > 0 {
			_, err = o.observeBatch(ctx, observations)
		}
	}

	if err != nil && ctx.Err() == nil && !o.quiet && o.errorWriter != nil {
		_, _ = fmt.Fprintf(o.errorWriter, "recording program status failed: %v\n", err)
	}
}

func programStatusObservations(
	sessions []registry.Session,
	panes []mux.Pane,
	at time.Time,
) []registry.Observation {
	var observations []registry.Observation

	for _, pane := range panes {
		session, ok := paneSession(sessions, pane.Location)
		if !ok {
			continue
		}

		observation, changed, _ := programStatusReading(session, session.Process, pane, at)
		if changed {
			observations = append(observations, observation)
		}
	}

	return observations
}

func paneSession(sessions []registry.Session, pane registry.Location) (registry.Session, bool) {
	var (
		found registry.Session
		count int
	)

	for _, session := range sessions {
		if session.Presence() == registry.PresenceLive && session.Process != nil &&
			(catalog.Rules{}).SamePane(session.Location, pane) {
			found = session
			count++
		}
	}

	// Which of several sessions owns the pane takes the process tree to decide,
	// so that case is left to the cycle.
	return found, count == 1
}

func programStatusWatch(opts Options) mux.ProgramStatusWatch {
	if opts.ProgramStatusWatch == nil && opts.PaneList == nil {
		return watchProgramStatus
	}

	return opts.ProgramStatusWatch
}

func watchProgramStatus(
	ctx context.Context,
	kind registry.MultiplexerKind,
	serverIdentity string,
	changed func([]mux.Pane),
) error {
	driver, _ := drivers.Find(kind)

	watcher, ok := driver.(mux.ProgramStatusWatcher)
	if !ok {
		return mux.ErrProgramStatusUnsignaled
	}

	if err := watcher.WatchProgramStatus(ctx, serverIdentity, changed); err != nil {
		return fmt.Errorf("watch %s program status: %w", kind, err)
	}

	return nil
}
