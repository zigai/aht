package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

const defaultWatchDebounce = 100 * time.Millisecond
const (
	watchFormatTable = "table"
	watchFormatPlain = "plain"
	watchFormatJSON  = "json"
)

const (
	watchActionAdded           = "added"
	watchActionRemoved         = "removed"
	watchActionSnapshot        = "snapshot"
	watchActionSnapshotEmpty   = "snapshot_empty"
	watchActionPresenceChanged = "presence_changed"
	watchActionActivityChanged = "activity_changed"
	watchActionProcessBound    = "process_bound"
	watchActionProcessGone     = "process_gone"
	watchActionLocationChanged = "location_changed"
	watchActionNativeEvent     = "native_event"
)

const (
	watchActivityOrder = 2
	watchProcessOrder  = watchActivityOrder + 1
	watchLocationOrder = watchProcessOrder + 1
	watchNativeOrder   = watchLocationOrder + 1
)

var (
	errWatchFormatJSONConflict    = errors.New("--format cannot be used with --json")
	errWatchStateDirectoryMissing = errors.New("watching store: state directory does not exist")
	errWatchStateDirectoryNotDir  = errors.New("watching store: state directory is not a directory")
	errInvalidWatchFormat         = errors.New("invalid watch format")
)

type watchOptions struct {
	filter     registry.Filter
	harness    string
	noSnapshot bool
	format     string
	formatSet  bool
	debounce   time.Duration
	now        func() time.Time
	ready      chan struct{}
}
type watchEvent struct {
	Time             time.Time          `json:"time"`
	Action           string             `json:"action"`
	ID               string             `json:"id,omitempty"`
	Harness          registry.Harness   `json:"harness,omitempty"`
	Presence         registry.Presence  `json:"presence,omitempty"`
	PreviousPresence registry.Presence  `json:"previous_presence,omitempty"`
	Activity         *registry.Activity `json:"activity,omitempty"`
	PreviousActivity *registry.Activity `json:"previous_activity,omitempty"`
	SessionID        string             `json:"session_id,omitempty"`
	SessionPath      string             `json:"session_path,omitempty"`
	Label            string             `json:"label,omitempty"`
	NativeEvent      string             `json:"native_event,omitempty"`
	CWD              string             `json:"cwd,omitempty"`
	Location         string             `json:"location,omitempty"`
}
type watchUpdateProcessor struct {
	app      *application
	options  watchOptions
	writer   *watchEventWriter
	previous []registry.Session
}

type watchEventWriter struct {
	app           *application
	format        string
	headerWritten bool
}

func (app *application) prepareWatch(o watchOptions) (watchOptions, error) {
	if o.formatSet && strings.TrimSpace(o.format) == "" {
		return o, fmt.Errorf("%w: empty value", errInvalidWatchFormat)
	}
	o = normalizeWatchOptions(o)
	if app.outputJSON {
		if o.formatSet {
			return o, errWatchFormatJSONConflict
		}
		o.format = watchFormatJSON
	}
	if !app.outputJSON && o.format != watchFormatTable && o.format != watchFormatPlain {
		return o, fmt.Errorf("%w: %q", errInvalidWatchFormat, o.format)
	}
	return o, nil
}

func (app *application) runWatch(ctx context.Context, o watchOptions) error {
	ahtClient := client.New(client.Config{StorePath: app.resolvedStorePath()})
	err := app.runBrokerWatch(ctx, o, ahtClient)
	if err == nil {
		return nil
	}
	if !client.IsUnavailable(err) {
		return fmt.Errorf("watching registry broker: %w", err)
	}

	return app.runFilesystemWatch(ctx, o)
}

func (app *application) runBrokerWatch(
	ctx context.Context,
	o watchOptions,
	ahtClient *client.Client,
) error {
	processor := newWatchUpdateProcessor(app, o)
	initialized := false
	err := ahtClient.Watch(ctx, o.filter, func(state registry.StateSnapshot) error {
		first := !initialized
		if first {
			initialized = true
		}
		return processor.accept(state.Sessions, first)
	})
	if err != nil {
		return fmt.Errorf("watching AHT state: %w", err)
	}
	return nil
}

func (app *application) runFilesystemWatch(ctx context.Context, o watchOptions) error {
	s := app.store()
	if _, _, e := watchTarget(s); e != nil {
		return e
	}
	processor := newWatchUpdateProcessor(app, o)
	err := s.Watch(ctx, registry.WatchOptions{
		Filter:            o.filter,
		Debounce:          o.debounce,
		ReconcileInterval: 0,
	}, func(result registry.WatchResult) error {
		if app.reportWatchResultError(result) {
			return nil
		}
		return processor.accept(result.Sessions, result.Initial)
	})
	if err != nil {
		return fmt.Errorf("watching registry store: %w", err)
	}
	return nil
}

func newWatchUpdateProcessor(app *application, o watchOptions) *watchUpdateProcessor {
	return &watchUpdateProcessor{
		app:      app,
		options:  o,
		writer:   &watchEventWriter{app: app, format: o.format},
		previous: nil,
	}
}

func (p *watchUpdateProcessor) accept(rawSessions []registry.Session, initial bool) error {
	currentSessions := applyConfigFilter(rawSessions, p.app.cfg.Filter, p.options.harness)
	if initial {
		p.previous = currentSessions
		if !p.options.noSnapshot {
			if err := p.writer.write(snapshotWatchEvents(p.previous, p.options.now())); err != nil {
				return err
			}
		}
		notifyWatchReady(p.options.ready)
		return nil
	}

	events := diffWatchEvents(
		watchSessionMap(p.previous),
		watchSessionMap(currentSessions),
		p.options.now(),
	)
	if err := p.writer.write(events); err != nil {
		return err
	}
	p.previous = currentSessions
	return nil
}

func (app *application) reportWatchResultError(result registry.WatchResult) bool {
	if result.Err == nil {
		return false
	}
	app.warnf("watch warning: %v\n", result.Err)
	return true
}

func normalizeWatchOptions(o watchOptions) watchOptions {
	if strings.TrimSpace(o.format) == "" {
		o.format = watchFormatTable
	}
	if o.debounce <= 0 {
		o.debounce = defaultWatchDebounce
	}
	if o.now == nil {
		o.now = func() time.Time { return time.Now().UTC() }
	}
	return o
}

func watchTarget(s *registry.Journal) (string, string, error) {
	p, e := filepath.Abs(s.Path())
	if e != nil {
		return "", "", fmt.Errorf("resolve watch target: %w", e)
	}
	d := filepath.Dir(p)
	i, e := os.Stat(d)
	if e != nil {
		if os.IsNotExist(e) {
			return "", "", fmt.Errorf("%w: %s", errWatchStateDirectoryMissing, d)
		}
		return "", "", fmt.Errorf("stat watch state directory: %w", e)
	}
	if !i.IsDir() {
		return "", "", fmt.Errorf("%w: %s", errWatchStateDirectoryNotDir, d)
	}
	return p, d, nil
}

func notifyWatchReady(c chan struct{}) {
	if c != nil {
		close(c)
	}
}

func watchSessionMap(s []registry.Session) map[string]registry.Session {
	m := map[string]registry.Session{}
	for _, v := range s {
		m[v.ID] = v
	}
	return m
}

func snapshotWatchEvents(s []registry.Session, at time.Time) []watchEvent {
	if len(s) == 0 {
		return []watchEvent{{Time: at.UTC(), Action: watchActionSnapshotEmpty}}
	}
	o := []watchEvent{}
	for _, v := range s {
		o = append(o, watchEventFromSession(watchActionSnapshot, v, registry.Session{}, at))
	}
	sortWatchEvents(o)
	return o
}

//nolint:cyclop // event diffing compares each independent session dimension
func diffWatchEvents(p, n map[string]registry.Session, at time.Time) []watchEvent {
	o := []watchEvent{}
	for id, v := range n {
		old, ok := p[id]
		if !ok {
			o = append(o, watchEventFromSession(watchActionAdded, v, registry.Session{}, at))
			continue
		}
		if v.Presence() != old.Presence() {
			o = append(o, watchEventFromSession(watchActionPresenceChanged, v, old, at))
			if v.Presence() == registry.PresenceGone {
				o = append(o, watchEventFromSession(watchActionProcessGone, v, old, at))
			}
		}
		if v.Presence() == registry.PresenceLive && v.Process != nil && old.Process == nil {
			o = append(o, watchEventFromSession(watchActionProcessBound, v, old, at))
		}
		if !activityEqual(v.Activity(), old.Activity()) {
			o = append(o, watchEventFromSession(watchActionActivityChanged, v, old, at))
		}
		if !multiplexerLocationEqual(v.Location, old.Location) {
			o = append(o, watchEventFromSession(watchActionLocationChanged, v, old, at))
		}
		if nativeEvent(v) != nativeEvent(old) {
			o = append(o, watchEventFromSession(watchActionNativeEvent, v, old, at))
		}
	}
	for id, v := range p {
		if _, ok := n[id]; !ok {
			o = append(o, watchEventFromSession(watchActionRemoved, v, registry.Session{}, at))
		}
	}
	sortWatchEvents(o)
	return o
}

func activityEqual(a, b *registry.Activity) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func multiplexerLocationEqual(a, b registry.Location) bool {
	a.WindowName, b.WindowName = "", ""
	a.TabName, b.TabName = "", ""
	a.WorkspaceName, b.WorkspaceName = "", ""
	a.PaneCurrentPath, b.PaneCurrentPath = "", ""
	a.PanePID, b.PanePID = 0, 0
	a.PaneTTY, b.PaneTTY = "", ""
	a.ClientTTY, b.ClientTTY = "", ""
	return a == b
}

func nativeEvent(s registry.Session) string {
	if s.Observations.Native == nil {
		return ""
	}
	return s.Observations.Native.Event
}

func watchEventFromSession(a string, s, p registry.Session, at time.Time) watchEvent {
	e := watchEvent{Time: at.UTC(), Action: a, ID: s.ID, Harness: s.Harness, Presence: s.Presence(), Activity: s.Activity(), SessionID: s.SessionID, SessionPath: s.SessionPath, Label: sessionDisplayLabel(s), NativeEvent: nativeEvent(s), CWD: s.CWD, Location: watchMultiplexerLabel(s.Location)}
	if !s.UpdatedAt.IsZero() {
		e.Time = s.UpdatedAt
	}
	if a == watchActionPresenceChanged {
		e.Time = s.PresenceChangedAt
		e.PreviousPresence = p.Presence()
	}
	if a == watchActionActivityChanged {
		e.Time = s.ActivityChangedAt
		e.PreviousActivity = p.Activity()
	}
	if e.Time.IsZero() {
		e.Time = at.UTC()
	}
	return e
}

func sortWatchEvents(e []watchEvent) {
	order := map[string]int{watchActionPresenceChanged: watchActivityOrder - 1, watchActionActivityChanged: watchActivityOrder, watchActionProcessBound: watchProcessOrder, watchActionProcessGone: watchProcessOrder, watchActionLocationChanged: watchLocationOrder, watchActionNativeEvent: watchNativeOrder}
	sort.SliceStable(e, func(i, j int) bool {
		if !e[i].Time.Equal(e[j].Time) {
			return e[i].Time.Before(e[j].Time)
		}
		if e[i].ID != e[j].ID {
			return e[i].ID < e[j].ID
		}
		return order[e[i].Action] < order[e[j].Action]
	})
}

func watchMultiplexerLabel(ctx registry.Location) string {
	if ctx.Empty() {
		return ""
	}
	parts := []string{string(ctx.Kind)}
	if session := multiplexerSessionLabel(ctx); session != "-" {
		parts = append(parts, session)
	}
	if container := multiplexerContainerLabel(ctx); container != "-" {
		parts = append(parts, container)
	}
	if ctx.PaneID != "" {
		parts = append(parts, ctx.PaneID)
	}
	return strings.Join(parts, ":")
}

func (w *watchEventWriter) write(e []watchEvent) error {
	if len(e) == 0 {
		return nil
	}
	for _, v := range e {
		switch w.format {
		case watchFormatJSON:
			if er := w.app.writeJSONLine(v); er != nil {
				return er
			}
		case watchFormatPlain:
			if er := w.app.writeln(formatWatchPlainEvent(v)); er != nil {
				return er
			}
		default:
			if er := w.writeTableEvent(v); er != nil {
				return er
			}
		}
	}
	return nil
}

func (w *watchEventWriter) writeTableEvent(event watchEvent) error {
	line := formatWatchTableEvent(event)
	header := formatWatchTableHeader()
	if max(text.StringWidth(line), text.StringWidth(header)) > w.app.maxLineWidth() {
		w.headerWritten = false
		label := event.Label
		if event.Action == watchActionSnapshotEmpty {
			label = "no sessions"
		}
		return w.app.writeHumanDetails([]humanDetail{
			{label: "Time", value: event.Time.UTC().Format(time.RFC3339)},
			{label: "Event", value: event.Action},
			{label: "Agent", value: string(event.Harness)},
			{label: "Presence", value: string(event.Presence)},
			{label: "Activity", value: activityString(event.Activity)},
			{label: "Session", value: label, wrap: wrapHumanSession},
		})
	}
	if !w.headerWritten {
		if err := w.app.writeln(header + "\n" + strings.Repeat("─", text.StringWidth(header))); err != nil {
			return err
		}
		w.headerWritten = true
	}
	return w.app.writeln(line)
}

func formatWatchTableHeader() string {
	return fmt.Sprintf("%-20s  %-18s  %-10s  %-8s  %-11s  %s", "Time", "Event", "Agent", "Presence", "Activity", "Session")
}

func formatWatchPlainEvent(e watchEvent) string {
	if e.Action == watchActionSnapshotEmpty {
		return e.Time.UTC().Format(time.RFC3339) + " snapshot_empty no sessions"
	}
	return sanitizeHumanText(strings.Join([]string{e.Time.UTC().Format(time.RFC3339), e.Action, string(e.Harness), string(e.Presence), formatActivity(e.Activity), "session=" + e.Label}, " "))
}

func formatWatchTableEvent(e watchEvent) string {
	if e.Action == watchActionSnapshotEmpty {
		return e.Time.UTC().Format(time.RFC3339) + "  snapshot_empty      no sessions"
	}
	return fmt.Sprintf("%s  %-18s  %-10s  %-8s  %-11s  %s", e.Time.UTC().Format(time.RFC3339), e.Action, e.Harness, e.Presence, formatActivity(e.Activity), sanitizeHumanText(e.Label))
}
