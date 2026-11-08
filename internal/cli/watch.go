package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/registry"

	"github.com/jedib0t/go-pretty/v6/text"
)

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
	errWatchFormatJSONConflict = errors.New("--format cannot be used with --json")
	errInvalidWatchFormat      = errors.New("invalid watch format")
)

//exhaustruct:optional
type watchOptions struct {
	filter     registry.Filter
	noSnapshot bool
	format     string
	formatSet  bool
	now        func() time.Time
	ready      chan struct{}
}
type watchEvent struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	//exhaustruct:optional
	ID string `json:"id,omitempty"`
	//exhaustruct:optional
	Harness registry.Harness `json:"harness,omitempty"`
	//exhaustruct:optional
	Presence registry.Presence `json:"presence,omitempty"`
	//exhaustruct:optional
	PreviousPresence registry.Presence `json:"previous_presence,omitempty"`
	//exhaustruct:optional
	Activity *registry.Activity `json:"activity,omitempty"`
	//exhaustruct:optional
	PreviousActivity *registry.Activity `json:"previous_activity,omitempty"`
	//exhaustruct:optional
	SessionID string `json:"session_id,omitempty"`
	//exhaustruct:optional
	SessionPath string `json:"session_path,omitempty"`
	//exhaustruct:optional
	Label string `json:"label,omitempty"`
	//exhaustruct:optional
	NativeEvent string `json:"native_event,omitempty"`
	//exhaustruct:optional
	CWD string `json:"cwd,omitempty"`
	//exhaustruct:optional
	Location string `json:"location,omitempty"`
}
type watchUpdateProcessor struct {
	app         *application
	options     watchOptions
	writer      *watchEventWriter
	previous    []registry.Session
	initialized bool
}

type watchEventWriter struct {
	app    *application
	format string
	//exhaustruct:optional
	headerWritten bool
}

func (app *application) prepareWatch(opts watchOptions) (watchOptions, error) {
	if opts.formatSet && strings.TrimSpace(opts.format) == "" {
		return opts, fmt.Errorf("%w: empty value", errInvalidWatchFormat)
	}

	opts = normalizeWatchOptions(opts)
	if app.outputJSON {
		if opts.formatSet {
			return opts, errWatchFormatJSONConflict
		}

		opts.format = watchFormatJSON
	}

	if !app.outputJSON && opts.format != watchFormatTable && opts.format != watchFormatPlain {
		return opts, fmt.Errorf("%w: %q", errInvalidWatchFormat, opts.format)
	}

	return opts, nil
}

func (app *application) runWatch(ctx context.Context, opts watchOptions) error {
	processor := newWatchUpdateProcessor(app, opts)

	err := app.registryStore().Watch(ctx, opts.filter, func(state registry.StateSnapshot) error {
		return processor.accept(state.Sessions)
	})
	if err != nil {
		return fmt.Errorf("watching AHT state: %w", err)
	}

	return nil
}

func newWatchUpdateProcessor(app *application, opts watchOptions) *watchUpdateProcessor {
	return &watchUpdateProcessor{
		app:         app,
		options:     opts,
		writer:      &watchEventWriter{app: app, format: opts.format},
		previous:    nil,
		initialized: false,
	}
}

func (p *watchUpdateProcessor) accept(rawSessions []registry.Session) error {
	currentSessions := applyConfigFilter(rawSessions, p.app.cfg.Filter, p.options.filter.Harness)
	if !p.initialized {
		p.previous = currentSessions
		if !p.options.noSnapshot {
			if err := p.writer.write(snapshotWatchEvents(p.previous, p.options.now())); err != nil {
				return err
			}
		}

		p.initialized = true
		if p.options.ready != nil {
			close(p.options.ready)
		}

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

func normalizeWatchOptions(opts watchOptions) watchOptions {
	if strings.TrimSpace(opts.format) == "" {
		opts.format = watchFormatTable
	}

	if opts.now == nil {
		opts.now = func() time.Time { return time.Now().UTC() }
	}

	return opts
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
		o = append(o, watchEventFromSession(watchActionSnapshot, v, at))
	}

	sortWatchEvents(o)

	return o
}

func diffWatchEvents(
	p map[string]registry.Session,
	n map[string]registry.Session,
	at time.Time,
) []watchEvent {
	o := []watchEvent{}

	for id, v := range n {
		old, ok := p[id]
		if !ok {
			o = append(o, watchEventFromSession(watchActionAdded, v, at))
			continue
		}

		o = append(o, changedSessionWatchEvents(old, v, at)...)
	}

	for id, v := range p {
		if _, ok := n[id]; !ok {
			o = append(o, watchEventFromSession(watchActionRemoved, v, at))
		}
	}

	sortWatchEvents(o)

	return o
}

func changedSessionWatchEvents(
	old registry.Session,
	v registry.Session,
	at time.Time,
) []watchEvent {
	o := []watchEvent{}

	if v.Presence() != old.Presence() {
		changed := watchEventFromSession(watchActionPresenceChanged, v, at)
		changed.PreviousPresence = old.Presence()

		o = append(o, changed)
		if v.Presence() == registry.PresenceGone {
			o = append(o, watchEventFromSession(watchActionProcessGone, v, at))
		}
	}

	if v.Presence() == registry.PresenceLive && v.Process != nil && old.Process == nil {
		o = append(o, watchEventFromSession(watchActionProcessBound, v, at))
	}

	if !activityEqual(v.Activity(), old.Activity()) {
		changed := watchEventFromSession(watchActionActivityChanged, v, at)
		changed.PreviousActivity = old.Activity()
		o = append(o, changed)
	}

	if !v.Location.SameAddress(old.Location) {
		o = append(o, watchEventFromSession(watchActionLocationChanged, v, at))
	}

	if nativeEvent(v) != nativeEvent(old) {
		o = append(o, watchEventFromSession(watchActionNativeEvent, v, at))
	}

	return o
}

func activityEqual(a *registry.Activity, b *registry.Activity) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

func nativeEvent(s registry.Session) string {
	if s.Observations.Native == nil {
		return ""
	}

	return s.Observations.Native.Event
}

func watchEventFromSession(a string, s registry.Session, at time.Time) watchEvent {
	e := watchEvent{
		Time:        at.UTC(),
		Action:      a,
		ID:          s.ID,
		Harness:     s.Harness,
		Presence:    s.Presence(),
		Activity:    s.Activity(),
		SessionID:   s.SessionID,
		SessionPath: s.SessionPath,
		Label:       sessionDisplayLabel(s),
		NativeEvent: nativeEvent(s),
		CWD:         s.CWD,
		Location:    watchMultiplexerLabel(s.Location),
	}
	if !s.UpdatedAt.IsZero() {
		e.Time = s.UpdatedAt
	}

	if a == watchActionPresenceChanged {
		e.Time = s.PresenceChangedAt
	}

	if a == watchActionActivityChanged {
		e.Time = s.ActivityChangedAt
	}

	if e.Time.IsZero() {
		e.Time = at.UTC()
	}

	return e
}

func sortWatchEvents(e []watchEvent) {
	order := map[string]int{
		watchActionPresenceChanged: watchActivityOrder - 1,
		watchActionActivityChanged: watchActivityOrder,
		watchActionProcessBound:    watchProcessOrder,
		watchActionProcessGone:     watchProcessOrder,
		watchActionLocationChanged: watchLocationOrder,
		watchActionNativeEvent:     watchNativeOrder,
	}

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

func watchMultiplexerLabel(location registry.Location) string {
	if location.Empty() {
		return ""
	}

	parts := []string{string(location.Kind)}
	if session := multiplexerSessionLabel(location); session != "-" {
		parts = append(parts, session)
	}

	if container := multiplexerContainerLabel(location); container != "-" {
		parts = append(parts, container)
	}

	if location.PaneID != "" {
		parts = append(parts, location.PaneID)
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
			{label: "Activity", value: formatActivity(event.Activity)},
			{label: "Session", value: label, wrap: wrapHumanSession},
		})
	}

	if !w.headerWritten {
		if err := w.app.writeln(
			header + "\n" + strings.Repeat("─", text.StringWidth(header)),
		); err != nil {
			return err
		}

		w.headerWritten = true
	}

	return w.app.writeln(line)
}

func formatWatchTableHeader() string {
	return fmt.Sprintf(
		"%-20s  %-18s  %-10s  %-8s  %-11s  %s",
		"Time",
		"Event",
		"Agent",
		"Presence",
		"Activity",
		"Session",
	)
}

func formatWatchPlainEvent(e watchEvent) string {
	if e.Action == watchActionSnapshotEmpty {
		return e.Time.UTC().Format(time.RFC3339) + " snapshot_empty no sessions"
	}

	return sanitizeHumanText(
		strings.Join(
			[]string{
				e.Time.UTC().Format(time.RFC3339),
				e.Action,
				string(e.Harness),
				string(e.Presence),
				formatActivity(e.Activity),
				"session=" + e.Label,
			},
			" ",
		),
	)
}

func formatWatchTableEvent(e watchEvent) string {
	if e.Action == watchActionSnapshotEmpty {
		return e.Time.UTC().Format(time.RFC3339) + "  snapshot_empty      no sessions"
	}

	return fmt.Sprintf(
		"%s  %-18s  %-10s  %-8s  %-11s  %s",
		e.Time.UTC().Format(time.RFC3339),
		e.Action,
		e.Harness,
		e.Presence,
		formatActivity(e.Activity),
		sanitizeHumanText(e.Label),
	)
}

func (app *application) newWatchCommand() *cobra.Command {
	options := listOptions{}

	var (
		noSnapshot  bool
		watchFormat string
		prepared    watchOptions
	)

	command := &cobra.Command{
		Use:           "watch",
		Short:         "Stream session changes",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("presence") && cfg.UI.DefaultPresence != "" {
				options.presence = cfg.UI.DefaultPresence
			}

			filter, err := buildFilter(options)
			if err != nil {
				return err
			}

			prepared, err = app.prepareWatch(watchOptions{
				filter:     filter,
				noSnapshot: noSnapshot,
				format:     watchFormat,
				formatSet:  cmd.Flags().Changed("format"),
			})

			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runWatch(cmd.Context(), prepared)
		},
	}
	flags := command.Flags()
	configureSessionFilterFlags(flags, &options)
	flags.BoolVar(&noSnapshot, "no-snapshot", false, "start with future changes only")
	flags.StringVar(&watchFormat, "format", "", "output format: `<table|plain>`")

	return command
}
