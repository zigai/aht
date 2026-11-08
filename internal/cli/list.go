package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/aht/v2/internal/config"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/pathmatch"

	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	errInvalidListSort = errors.New("invalid list sort")
	errListSummaryFlag = errors.New(
		"--sort, --desc, and --absolute-time are not valid with --summary",
	)
	errListAbsoluteJSON          = errors.New("--absolute-time cannot be used with --json")
	errListGroupByWithoutSummary = errors.New("--group-by requires --summary")
	errInvalidMultiplexerKind    = errors.New("invalid multiplexer kind")
)

//exhaustruct:optional
type listOptions struct {
	harness, presence, activity, tmuxSession, multiplexerSession, sortBy, groupBy string
	project, cwd, multiplexerKind, multiplexerServer, multiplexerPane             string
	projectSubtree                                                                bool
	summary, absoluteTime, absoluteSet, sortSet, desc, descSet, full, groupBySet  bool
}

func (app *application) newListCommand() *cobra.Command {
	o := listOptions{}

	var filter registry.Filter

	cmd := &cobra.Command{
		Use:           listCommandName,
		Short:         "Show known sessions",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(c *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}

			applyListConfig(&o, c, cfg)

			if err := app.validateListOptions(o); err != nil {
				return exitCode(err, exitCodeUsage)
			}

			filter, err = buildFilter(o)

			return exitCode(err, exitCodeUsage)
		},
		RunE: func(c *cobra.Command, _ []string) error {
			if o.summary {
				return app.runListSummary(c.Context(), o, filter)
			}

			return app.runListSessions(c.Context(), o, filter)
		},
	}
	f := cmd.Flags()
	configureSessionFilterFlags(f, &o)
	f.StringVar(
		&o.sortBy,
		"sort",
		"",
		"sort by: `<field>` (updated, created, harness, presence, "+
			"activity, cwd, id, multiplexer, tmux, presence-changed, "+
			"activity-changed)",
	)
	f.BoolVar(&o.summary, "summary", false, "summarize agent counts by multiplexer session")
	f.StringVar(
		&o.groupBy,
		"group-by",
		"",
		"group summaries by: `<field>` (multiplexer-session, project, harness)",
	)
	f.BoolVar(&o.absoluteTime, "absolute-time", false, "show full timestamps")
	f.BoolVar(&o.desc, "desc", false, "sort descending")
	f.BoolVar(&o.full, "full", false, "show complete values using an adaptive layout")

	return cmd
}

func configureSessionFilterFlags(f *pflag.FlagSet, opts *listOptions) {
	f.StringVar(&opts.harness, "harness", "", "filter by harness `<name>`")
	f.StringVar(
		&opts.presence,
		"presence",
		"",
		"filter by presence `<val>`: live, gone, unknown, all",
	)
	f.StringVar(
		&opts.activity,
		"activity",
		"",
		"filter by activity `<val>`: running, waiting, idle, unknown",
	)
	f.StringVar(&opts.tmuxSession, "tmux-session", "", "filter by tmux session `<name>`")
	f.StringVar(
		&opts.multiplexerSession,
		"multiplexer-session",
		"",
		"filter by multiplexer session `<name>`",
	)
	f.StringVar(&opts.project, "project", "", "filter by project `<dir>`")
	f.BoolVar(
		&opts.projectSubtree,
		"project-subtree",
		false,
		"include sessions within project subtrees",
	)
	f.StringVar(&opts.cwd, "cwd", "", "filter by session working directory `<dir>`")
	f.StringVar(
		&opts.multiplexerKind,
		"multiplexer",
		"",
		"filter by multiplexer `<kind>`: tmux, zellij, herdr",
	)
	f.StringVar(&opts.multiplexerServer, "server", "", "filter by multiplexer server `<id>`")
	f.StringVar(&opts.multiplexerPane, "pane", "", "filter by multiplexer pane `<id>`")
}

func applyListConfig(opts *listOptions, cmd *cobra.Command, cfg config.Config) {
	f := cmd.Flags()
	if !f.Changed("presence") && cfg.UI.DefaultPresence != "" {
		opts.presence = cfg.UI.DefaultPresence
	}

	if !f.Changed("sort") && cfg.UI.Sort != "" {
		opts.sortBy = cfg.UI.Sort
	}

	opts.sortSet = f.Changed("sort")
	if !f.Changed("desc") && cfg.UI.SortDesc != nil {
		opts.desc = *cfg.UI.SortDesc
	}

	opts.descSet = f.Changed("desc")

	opts.groupBySet = f.Changed("group-by")
	if !f.Changed("absolute-time") {
		if (cfg.UI.AbsoluteTime != nil && *cfg.UI.AbsoluteTime) ||
			cfg.UI.TimeFormat == "absolute" || cfg.UI.TimeFormat == "iso8601" {
			opts.absoluteTime = true
		}
	} else {
		opts.absoluteSet = f.Changed("absolute-time")
	}
}

func (app *application) validateListOptions(opts listOptions) error {
	if app.outputJSON && opts.absoluteSet {
		return errListAbsoluteJSON
	}

	if err := validateListSummaryOptions(opts); err != nil {
		return err
	}

	if opts.summary {
		return nil
	}

	if opts.sortSet && strings.TrimSpace(opts.sortBy) == "" {
		return fmt.Errorf("%w: empty value", errInvalidListSort)
	}

	_, err := listSortKey(opts.sortBy)

	return err
}

func validateListSummaryOptions(opts listOptions) error {
	if opts.summary && (opts.absoluteSet || opts.sortSet || opts.descSet) {
		return errListSummaryFlag
	}

	if (opts.groupBySet || opts.groupBy != "") && !opts.summary {
		return errListGroupByWithoutSummary
	}

	if opts.groupBySet || opts.groupBy != "" {
		switch opts.groupBy {
		case "multiplexer-session", "project", "harness":
			// valid
		default:
			return fmt.Errorf(
				"%w: %q (expected multiplexer-session, project, or harness)",
				registry.ErrUnsupportedGroupBy,
				opts.groupBy,
			)
		}
	}

	return nil
}

func buildFilter(opts listOptions) (registry.Filter, error) {
	f := registry.Filter{
		MultiplexerSession: cmp.Or(opts.multiplexerSession, opts.tmuxSession),
		Project:            opts.project,
		ProjectSubtree:     opts.projectSubtree,
		CWD:                opts.cwd,
		MultiplexerServer:  opts.multiplexerServer,
		MultiplexerPane:    opts.multiplexerPane,
	}
	if opts.multiplexerKind != "" {
		kind := registry.MultiplexerKind(strings.ToLower(strings.TrimSpace(opts.multiplexerKind)))
		switch kind {
		case registry.MultiplexerTmux, registry.MultiplexerZellij, registry.MultiplexerHerdr:
			f.MultiplexerKind = kind
		default:
			return f, fmt.Errorf("%w: %q", errInvalidMultiplexerKind, opts.multiplexerKind)
		}
	}

	if opts.harness != "" {
		h, e := catalog.Parse(opts.harness)
		if e != nil {
			return f, fmt.Errorf("normalize harness: %w", e)
		}

		f.Harness = h
	}

	if opts.presence != "" {
		if strings.EqualFold(opts.presence, "all") {
			f.Presence = ""
		} else {
			p, e := registry.NormalizePresence(opts.presence)
			if e != nil {
				return f, fmt.Errorf("normalize presence: %w", e)
			}

			f.Presence = p
		}
	}

	if opts.activity != "" {
		a, e := registry.NormalizeActivity(opts.activity)
		if e != nil {
			return f, fmt.Errorf("normalize activity: %w", e)
		}

		f.Activity = a
	}

	return f, nil
}

func (app *application) runListSessions(
	ctx context.Context,
	opts listOptions,
	f registry.Filter,
) error {
	ss, e := app.registryStore().List(ctx, f)
	if e != nil {
		return e //nolint:wrapcheck // Client.List supplies the operation context.
	}

	ss = applyConfigFilter(ss, app.cfg.Filter, f.Harness)
	if e = sortListSessions(ss, opts); e != nil {
		return exitCode(e, exitCodeUsage)
	}

	if app.outputJSON {
		return app.writeJSON(ss)
	}

	now := time.Now().UTC()

	var displayIDs map[string]string
	if !opts.full {
		displayIDs = abbreviatedRegistryIDs(ss)
	}

	rows := make([][]string, 0, len(ss))
	for _, s := range ss {
		id := s.ID
		if !opts.full {
			id = displayIDs[s.ID]
		}

		rows = append(rows, []string{
			id,
			string(s.Harness),
			sessionDisplayLabel(s),
			string(s.Presence()),
			listActivity(s),
			watchMultiplexerLabel(
				s.Location,
			),
			formatHumanPath(s.CWD),
			formatUpdatedAt(s.UpdatedAt, now, opts.absoluteTime),
		})
	}

	maxWidth := app.maxLineWidth()
	if opts.full {
		columns, fits := listFullTableColumns(rows, maxWidth)
		if !fits {
			return app.writeStackedHumanRows(columns, rows)
		}

		return app.writeWrappedHumanTable(columns, rows)
	}

	return app.writeHumanTable(listTableColumns(rows, maxWidth), rows)
}

func applyConfigFilter(
	sessions []registry.Session,
	filter config.FilterConfig,
	explicit registry.Harness,
) []registry.Session {
	if len(sessions) == 0 {
		return sessions
	}

	ignored := configuredIgnoreHarnesses(filter.IgnoreHarnesses, []registry.Harness{explicit})

	result := make([]registry.Session, 0, len(sessions))
	for _, s := range sessions {
		if slices.Contains(ignored, s.Harness) ||
			pathmatch.MatchAny(filter.IgnorePaths, s.CWD, s.ProjectRoot) {
			continue
		}

		result = append(result, s)
	}

	return result
}
