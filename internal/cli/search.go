package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/history"
	"github.com/zigai/aht/pkg/registry"
)

var (
	errSearchIncomplete    = errors.New("search incomplete; some histories could not be searched")
	errSearchSource        = errors.New("invalid --source")
	errSearchAgentConflict = errors.New("conflicting --agent and --source harnesses")
	errSearchArgumentCount = errors.New("expected exactly one text argument")
)

type searchOptions struct {
	query   history.Query
	agent   string
	sources []string
}

func (app *application) newSearchCommand() *cobra.Command {
	var options searchOptions
	var sources []history.Source
	command := &cobra.Command{
		Use:           "search <text>",
		Short:         "Search retained conversations across coding-agent harnesses",
		Long:          "Search local conversation content, including sessions created before AHT was installed.\nMatches are literal and case-insensitive by default. System and reasoning content are excluded.\nMatches are ordered by most recently updated conversation first, and --limit keeps the newest <count>.\nResults include source coverage; unreadable history returns partial results with exit code 1.",
		Example:       "  aht search \"refresh token\"\n  aht search \"refresh token\" --agent codex --dir ~/Projects/myapp\n  aht search \"connection refused\" --include-tools --json\n  aht search \"migration\" --source omp=/archive/omp/sessions",
		SilenceErrors: true, SilenceUsage: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return exitCode(fmt.Errorf("search: %w, received %d; for example aht search %q; run aht search --help", errSearchArgumentCount, len(args), "refresh token"), exitCodeUsage)
			}
			return nil
		},
		PreRunE: func(_ *cobra.Command, args []string) error {
			options.query.Text = args[0]
			var err error
			sources, err = options.validate()
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}
			_, err = app.loadConfig()
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runSearch(cmd.Context(), options, sources)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.agent, "agent", "", "filter by harness `<name>`")
	flags.StringVar(&options.query.Dir, "dir", "", "match recorded working directories or workspace roots under `<path>`")
	flags.BoolVar(&options.query.IncludeTools, "include-tools", false, "also search tool calls and tool output")
	flags.BoolVar(&options.query.CaseSensitive, "case-sensitive", false, "match text with exact case")
	flags.IntVar(&options.query.Limit, "limit", 0, "return at most `<count>` matching conversations, newest first (0 means unlimited)")
	flags.StringArrayVar(&options.sources, "source", nil, "search only this native history `<agent=path>` (repeatable)")
	return command
}

func (options *searchOptions) validate() ([]history.Source, error) {
	if options.agent != "" {
		id, err := harness.Parse(options.agent)
		if err != nil {
			return nil, fmt.Errorf("search agent: %w", err)
		}
		options.query.Harness = id
	}
	if err := options.query.Validate(); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	// Sources must stay nil when no --source was given: the library reads a
	// non-nil empty slice as an explicit selection of nothing.
	var sources []history.Source
	for _, value := range options.sources {
		name, path, ok := strings.Cut(value, "=")
		if !ok || path == "" {
			return nil, fmt.Errorf("search: %w %q: expected agent=path, for example --source codex=/path/to/sessions", errSearchSource, value)
		}
		id, err := harness.Parse(name)
		if err != nil {
			return nil, fmt.Errorf("search source: %w", err)
		}
		if options.query.Harness != "" && id != options.query.Harness {
			return nil, fmt.Errorf("search: %w: --agent %q and --source %q select different harnesses; see aht search --help", errSearchAgentConflict, options.agent, value)
		}
		sources = append(sources, history.Source{Harness: id, Path: path})
	}
	return sources, nil
}

func (app *application) runSearch(ctx context.Context, options searchOptions, sources []history.Source) error {
	cfg := app.cfg
	options.query.IgnoreHarnesses = configuredIgnoreHarnesses(cfg.Filter.IgnoreHarnesses, sources)
	options.query.IgnorePaths = cfg.Filter.IgnorePaths
	// History search works without creating a live registry or observer lock.
	if _, statErr := os.Stat(app.resolvedStorePath()); statErr == nil {
		sessions, listErr := app.registryStore().List(ctx, registry.Filter{})
		if listErr == nil {
			options.query.Registry = sessions
		} else {
			app.warnf("warning: live status unavailable: %s\n", sanitizeHumanText(listErr.Error()))
		}
	}
	catalog := history.Catalog{Sources: sources}
	result, searchErr := catalog.Search(ctx, options.query)
	if app.outputJSON {
		if err := app.writeSearchJSON(result); err != nil {
			return err
		}
	} else if err := app.writeSearchResults(result, searchErr); err != nil {
		return err
	}
	return searchFailure(searchErr)
}

// configuredIgnoreHarnesses applies filter.ignore_harnesses while keeping an
// explicit --source selection authoritative: an explicitly selected harness is
// never silently dropped by configuration, and every other ignored harness stays
// ignored.
func configuredIgnoreHarnesses(configured []string, sources []history.Source) []registry.Harness {
	explicit := make([]registry.Harness, 0, len(sources))
	for _, source := range sources {
		explicit = append(explicit, source.Harness)
	}
	ignored := make([]registry.Harness, 0, len(configured))
	for _, name := range configured {
		id, err := harness.Parse(name)
		if err != nil || slices.Contains(explicit, id) {
			continue
		}
		ignored = append(ignored, id)
	}
	return ignored
}

// searchFailure maps a history search error onto the CLI contract: an incomplete
// search is one concise line with exit code 1, cancellation keeps its context
// error so the process still exits 130, and any other failure stays wrapped.
func searchFailure(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("search history: %w", err)
	case errors.Is(err, history.ErrIncomplete):
		return exitCode(errSearchIncomplete, exitCodeGeneral)
	default:
		return fmt.Errorf("search history: %w", err)
	}
}

func (app *application) writeSearchResults(result history.Result, searchErr error) error {
	if len(result.Matches) == 0 {
		// A definitive empty state is only honest when every selected source was
		// searched; a partial search must not look like a confirmed absence.
		empty := "No matching conversations."
		if searchErr != nil {
			empty = "No matching conversations; some sources could not be searched."
		}
		if err := app.writeln(empty); err != nil {
			return err
		}
	}
	for _, match := range result.Matches {
		if err := app.writeSearchMatch(match); err != nil {
			return err
		}
	}
	for _, issue := range result.Issues {
		app.warnf("warning: %s %s: %s\n", issue.Source.Harness, sanitizeHumanText(issue.Path), sanitizeHumanText(issue.Message))
	}
	if result.Truncated {
		app.warnf("More matching conversations exist; increase or omit --limit, or narrow --agent/--dir.\n")
	}
	var unsupported []string
	for _, source := range result.Sources {
		if source.Status != "unsupported" {
			continue
		}
		// Several sources can name one harness; the summary lists it once.
		if name := string(source.Source.Harness); !slices.Contains(unsupported, name) {
			unsupported = append(unsupported, name)
		}
	}
	if len(unsupported) > 0 {
		app.warnf("History readers unavailable: %s.\n", strings.Join(unsupported, ", "))
	}
	return nil
}

func (app *application) writeSearchMatch(match history.Match) error {
	c := match.Conversation
	state := searchPresence(match.Live)
	if err := app.writef("%s %s [%s]\n", c.Harness, sanitizeHumanText(c.SessionID), state); err != nil {
		return err
	}
	details := []humanDetail{{label: "Title", value: c.Title}, {label: "CWD", value: c.CWD}, {label: "History", value: c.Path}}
	if !c.UpdatedAt.IsZero() {
		details = append(details, humanDetail{label: "Updated", value: c.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	if err := app.writeHumanDetails(details); err != nil {
		return err
	}
	for _, excerpt := range match.Excerpts {
		if err := app.writeHumanDetails([]humanDetail{{label: excerpt.Role, value: excerpt.Text}}); err != nil {
			return err
		}
	}

	return app.writeln()
}

func (app *application) writeSearchJSON(result history.Result) error {
	data, err := json.MarshalIndent(result, "", jsonIndent)
	if err != nil {
		return fmt.Errorf("encode history results: %w", err)
	}
	var safe strings.Builder
	safe.Grow(len(data))
	for _, character := range string(data) {
		if isBidiControl(character) {
			quoted := strconv.QuoteRuneToASCII(character)
			safe.WriteString(quoted[1 : len(quoted)-1])
		} else {
			safe.WriteRune(character)
		}
	}
	return app.writeln(safe.String())
}

func searchPresence(evidence []history.LiveState) string {
	allGone := len(evidence) > 0
	for _, state := range evidence {
		if state.Presence == registry.PresenceLive {
			return "live (last observed)"
		}
		if state.Presence != registry.PresenceGone {
			allGone = false
		}
	}
	if allGone {
		return "gone (last observed)"
	}
	return "unknown"
}
