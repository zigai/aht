package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	harnessBadgeWidth      = 7
	maxRuleWidth           = 80
	minRuleWidth           = 20
	metaIndentWidth        = 2
	minListColumnsWidth    = 2 * minRuleWidth
	maxSummarizedIssues    = 3
	maxRoleLabelWidth      = 12
	searchProgressInterval = 100 * time.Millisecond
	searchTitleShare       = 3
	searchShareTotal       = 5
	highlightStart         = "\x1b[1;36m"
	styleReset             = "\x1b[0m"
)

var (
	errSearchIncomplete    = errors.New("search incomplete; some histories could not be searched")
	errSearchSource        = errors.New("invalid --source")
	errSearchAgentConflict = errors.New("conflicting --harness and --source harnesses")
	errSearchRole          = errors.New("invalid --role")
	errSearchTime          = errors.New("invalid time")
	errSearchOption        = errors.New("invalid option")
	errSearchStream        = errors.New("--stream reports matches as they are found and cannot be combined with --sort, --limit, or --group-by")
)

type searchOptions struct {
	query       history.Query
	harnesses   []string
	role        string
	since       string
	until       string
	presence    string
	sort        string
	groupBy     string
	format      string
	sources     []string
	stream      bool
	verbose     bool
	listing     bool
	minMessages int
}

// searchProgress shows a transient scan status on an interactive stderr while
// the result is still being computed.
type searchProgress struct {
	app     *application
	enabled bool
	mu      sync.Mutex
	last    time.Time
	shown   bool
}

// excerptCell is one displayed rune of an excerpt and whether a match covers it.
type excerptCell struct {
	character rune
	width     int
	match     bool
}

type searchGroup struct {
	Group         string `json:"group"`
	Conversations int    `json:"conversations"`
	Messages      int    `json:"messages"`
}

func (app *application) newSearchCommand() *cobra.Command {
	var options searchOptions
	var sources []history.Source
	command := &cobra.Command{
		Use:   "search [text...]",
		Short: "Search or list retained conversations across coding-agent harnesses",
		Long: "Search local conversation content, including sessions created before AHT was installed.\n" +
			"Every text argument must appear in a conversation; --not excludes conversations.\n" +
			"Matches are literal and case-insensitive by default; --regex and --word change matching.\n" +
			"Without text, lists conversations selected by the filters.\n" +
			"System and reasoning content are excluded. Results are ordered by --sort, most recent activity first by default.\n" +
			"Unreadable history returns partial results with exit code 1; skipped malformed records are reported as warnings.",
		Example: "  aht search \"refresh token\"\n" +
			"  aht search token refresh --not oauth --harness codex,claude --since 7d\n" +
			"  aht search --dir ~/Projects/myapp --since 2w\n" +
			"  aht search 'retry(ing)?' --regex --dir ~/Projects/myapp --format resume\n" +
			"  aht search --group-by project --since 30d\n" +
			"  aht search \"connection refused\" --include-tools --stream --json",
		SilenceErrors: true, SilenceUsage: true,
		Args: cobra.ArbitraryArgs,
		PreRunE: func(_ *cobra.Command, args []string) error {
			options.query.Terms = args
			var err error
			sources, err = options.validate(time.Now())
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
	flags.StringSliceVar(&options.harnesses, "harness", nil, "filter by harness `<name,...>` (repeatable)")
	flags.StringSliceVar(&options.harnesses, "agent", nil, "filter by harness `<name,...>` (alias for --harness)")
	_ = flags.MarkHidden("agent")
	flags.StringVar(&options.query.Dir, "dir", "", "match recorded working directories or workspace roots under `<path>`")
	flags.StringArrayVar(&options.query.ExcludeDirs, "exclude-dir", nil, "skip conversations under `<path>` (repeatable)")
	flags.StringVar(&options.since, "since", "", "keep conversations active since `<when>`: a duration such as 12h or 7d, a date, or RFC3339")
	flags.StringVar(&options.until, "until", "", "keep conversations started before `<when>`: a duration, a date, or RFC3339")
	flags.StringVar(&options.presence, "presence", "", "keep conversations whose tracked session is `<live|gone>`")
	flags.StringVar(&options.query.GitBranch, "branch", "", "keep conversations recorded on git branch `<name>`")
	flags.StringVar(&options.query.Model, "model", "", "keep conversations whose model contains `<text>`")
	flags.IntVar(&options.minMessages, "min-messages", 1, "keep conversations with at least `<count>` user and assistant messages")
	flags.StringArrayVar(&options.query.Exclude, "not", nil, "exclude conversations containing `<text>` (repeatable)")
	flags.BoolVar(&options.query.Regex, "regex", false, "treat text and --not values as Go regular expressions")
	flags.BoolVar(&options.query.Word, "word", false, "match whole words only")
	flags.BoolVar(&options.query.IncludeTools, "include-tools", false, "also search tool calls and tool output")
	flags.BoolVar(&options.query.CaseSensitive, "case-sensitive", false, "match text with exact case")
	flags.StringVar(&options.role, "role", "", "search only messages from this role `<user|agent|tool|all>` (default: user and agent)")
	flags.IntVar(&options.query.Excerpts, "excerpts", 0, "show at most `<count>` excerpts per conversation (default 3)")
	flags.StringVar(&options.sort, "sort", "", "order by `<updated|created|matches|messages>` (default updated)")
	flags.IntVar(&options.query.Limit, "limit", 0, "return at most `<count>` conversations in sort order (0 means unlimited)")
	flags.BoolVar(&options.stream, "stream", false, "print matches as they are found instead of sorted")
	flags.StringVar(&options.groupBy, "group-by", "", "count conversations by `<project|harness|day>`")
	flags.StringVar(&options.format, "format", "", "print one `<id|path|resume>` per conversation")
	flags.BoolVarP(&options.verbose, "verbose", "v", false, "show detailed file paths and all diagnostic warnings")
	flags.StringArrayVar(&options.sources, "source", nil, "search only this native history `<agent=path>` (repeatable)")
	return command
}

func (options *searchOptions) validate(now time.Time) ([]history.Source, error) {
	options.listing = len(options.query.Terms) == 0
	if err := options.validateFilters(now); err != nil {
		return nil, err
	}
	if err := options.validateOutput(); err != nil {
		return nil, err
	}
	if options.listing {
		if err := options.listQuery().Validate(); err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
	} else {
		if err := options.validateRole(); err != nil {
			return nil, err
		}
		if err := options.query.Validate(); err != nil {
			return nil, fmt.Errorf("search: %w", err)
		}
	}
	return options.validateSources()
}

func (options *searchOptions) validateFilters(now time.Time) error {
	for _, name := range options.harnesses {
		id, err := harness.Parse(strings.TrimSpace(name))
		if err != nil {
			return fmt.Errorf("search harness: %w", err)
		}
		options.query.Harnesses = append(options.query.Harnesses, id)
	}
	var err error
	if options.query.Since, err = parseSearchTime(options.since, now); err != nil {
		return fmt.Errorf("search --since: %w", err)
	}
	if options.query.Until, err = parseSearchTime(options.until, now); err != nil {
		return fmt.Errorf("search --until: %w", err)
	}
	switch options.presence {
	case "", string(registry.PresenceLive), string(registry.PresenceGone):
		options.query.Presence = registry.Presence(options.presence)
	default:
		return fmt.Errorf("search: %w --presence %q; choose live or gone", errSearchOption, options.presence)
	}
	options.query.MinMessages = options.minMessages
	options.query.Sort = history.Sort(options.sort)
	return nil
}

func (options *searchOptions) validateOutput() error {
	switch options.groupBy {
	case "", "project", "harness", "day":
	default:
		return fmt.Errorf("search: %w --group-by %q; choose project, harness, or day", errSearchOption, options.groupBy)
	}
	switch options.format {
	case "", "id", "path", "resume":
	default:
		return fmt.Errorf("search: %w --format %q; choose id, path, or resume", errSearchOption, options.format)
	}
	if options.groupBy != "" && options.format != "" {
		return fmt.Errorf("search: %w: --group-by and --format select different outputs", errSearchOption)
	}
	if options.stream && (options.sort != "" || options.query.Limit != 0 || options.groupBy != "") {
		return fmt.Errorf("search: %w", errSearchStream)
	}
	return nil
}

func (options *searchOptions) validateSources() ([]history.Source, error) {
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
		if len(options.query.Harnesses) > 0 && !slices.Contains(options.query.Harnesses, id) {
			return nil, fmt.Errorf("search: %w: --source %q; see aht search --help", errSearchAgentConflict, value)
		}
		sources = append(sources, history.Source{Harness: id, Path: path})
	}
	return sources, nil
}

func (options *searchOptions) validateRole() error {
	if options.role == "" {
		return nil
	}
	role := strings.ToLower(strings.TrimSpace(options.role))
	switch role {
	case "user":
		options.query.Role = "user"
	case "agent", "assistant":
		options.query.Role = "assistant"
	case "tool":
		options.query.Role = "tool"
	case "all":
		options.query.Role = ""
	default:
		return fmt.Errorf("search: %w %q; choose user, agent, tool, or all", errSearchRole, options.role)
	}
	return nil
}

func (options *searchOptions) listQuery() history.ListQuery {
	return history.ListQuery{Filter: options.query.Filter, Sort: options.query.Sort, Limit: options.query.Limit}
}

// parseSearchTime reads a relative duration before now, a local date, or an
// RFC3339 timestamp.
func parseSearchTime(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if at, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil { //nolint:gosmopolitan // --since dates are the user's local calendar days
		return at, nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return at, nil
	}
	ago, err := config.ParseDuration(value)
	if err != nil || ago <= 0 {
		return time.Time{}, fmt.Errorf("%w %q: use a duration such as 12h or 7d, a date such as 2026-09-01, or RFC3339", errSearchTime, value)
	}
	return now.Add(-ago), nil
}

func (app *application) runSearch(ctx context.Context, opts searchOptions, sources []history.Source) error {
	cfg := app.cfg
	opts.query.IgnoreHarnesses = configuredIgnoreHarnesses(cfg.Filter.IgnoreHarnesses, sources)
	opts.query.IgnorePaths = cfg.Filter.IgnorePaths
	// History search works without creating a live registry or observer lock.
	if _, statErr := os.Stat(app.resolvedStorePath()); statErr == nil {
		sessions, listErr := app.registryStore().List(ctx, registry.Filter{})
		if listErr == nil {
			opts.query.Registry = sessions
		} else {
			app.warnf("warning: live status unavailable: %s\n", sanitizeHumanText(listErr.Error()))
		}
	}
	progress := app.newSearchProgress()
	catalog := history.Catalog{Sources: sources, IndexPath: "", Progress: progress.update}
	if opts.stream {
		return app.runSearchStream(ctx, catalog, opts, progress)
	}
	var result history.Result
	var searchErr error
	if opts.listing {
		result, searchErr = catalog.List(ctx, opts.listQuery())
	} else {
		result, searchErr = catalog.Search(ctx, opts.query)
	}
	progress.clear()
	if err := app.writeSearchOutput(result, searchErr, opts); err != nil {
		return err
	}
	return searchFailure(searchErr)
}

func (app *application) writeSearchOutput(result history.Result, searchErr error, opts searchOptions) error {
	switch {
	case opts.groupBy != "":
		if err := app.writeSearchGroups(result.Matches, opts.groupBy); err != nil {
			return err
		}
	case opts.format != "":
		for _, match := range result.Matches {
			if err := app.writeSearchField(match, opts.format); err != nil {
				return err
			}
		}
	case app.outputJSON:
		return app.writeSearchJSON(result)
	case opts.listing:
		if err := app.writeSearchList(result.Matches); err != nil {
			return err
		}
	default:
		if err := app.writeSearchResults(result.Matches, searchErr, opts); err != nil {
			return err
		}
	}
	app.writeSearchDiagnostics(result, opts)
	return nil
}

func (app *application) runSearchStream(ctx context.Context, catalog history.Catalog, opts searchOptions, progress *searchProgress) error {
	tty := app.isColorEnabled()
	var writeErr error
	count := 0
	yield := func(match history.Match) bool {
		progress.clear()
		count++
		writeErr = app.writeStreamMatch(match, opts, tty)
		return writeErr == nil
	}
	var result history.Result
	var searchErr error
	if opts.listing {
		// Listing has no text to stream; it streams the sorted listing instead.
		result, searchErr = catalog.List(ctx, opts.listQuery())
		for _, match := range result.Matches {
			if !yield(match) {
				break
			}
		}
	} else {
		result, searchErr = catalog.Stream(ctx, opts.query, yield)
	}
	progress.clear()
	if writeErr != nil {
		return writeErr
	}
	if count == 0 && !app.outputJSON && opts.format == "" {
		if err := app.writeln(emptySearchMessage(searchErr, opts.listing)); err != nil {
			return err
		}
	}
	app.writeSearchDiagnostics(result, opts)
	return searchFailure(searchErr)
}

func (app *application) writeStreamMatch(match history.Match, opts searchOptions, tty bool) error {
	switch {
	case opts.format != "":
		return app.writeSearchField(match, opts.format)
	case app.outputJSON:
		return app.writeSearchJSONLine(match)
	case tty:
		return app.writeSearchMatchTTY(match, opts.verbose)
	default:
		return app.writeSearchMatch(match)
	}
}

func (app *application) newSearchProgress() *searchProgress {
	enabled := !app.outputJSON && terminalWidth(app.stderr) > 0
	return &searchProgress{app: app, enabled: enabled, mu: sync.Mutex{}, last: time.Time{}, shown: false}
}

func (p *searchProgress) update(progress history.Progress) {
	if !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if now.Sub(p.last) < searchProgressInterval {
		return
	}
	p.last = now
	status := fmt.Sprintf("Scanning %s history %d/%d", progress.Source.Harness, progress.Done, progress.Total)
	if progress.Refreshed > 0 {
		status += fmt.Sprintf(", indexed %d changed", progress.Refreshed)
	}
	p.app.warnf("\r\x1b[2K%s", status)
	p.shown = true
}

func (p *searchProgress) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.shown {
		p.app.warnf("\r\x1b[2K")
		p.shown = false
	}
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

func emptySearchMessage(searchErr error, listing bool) string {
	empty := "No matching conversations."
	if listing {
		empty = "No conversations."
	}
	if searchErr != nil {
		empty = strings.TrimSuffix(empty, ".") + "; some sources could not be searched."
	}
	return empty
}

func (app *application) writeSearchResults(matches []history.Match, searchErr error, opts searchOptions) error {
	if len(matches) == 0 {
		return app.writeln(emptySearchMessage(searchErr, false))
	}
	isTTY := app.isColorEnabled()
	for _, match := range matches {
		var err error
		if isTTY {
			err = app.writeSearchMatchTTY(match, opts.verbose)
		} else {
			err = app.writeSearchMatch(match)
		}
		if err != nil {
			return err
		}
	}
	if isTTY {
		ruleWidth := min(maxRuleWidth, max(minRuleWidth, app.maxLineWidth()))
		return app.writeln("\x1b[2m" + strings.Repeat("─", ruleWidth) + styleReset)
	}
	return nil
}

func (app *application) writeSearchDiagnostics(result history.Result, opts searchOptions) {
	app.writeSearchIssues(result, opts.verbose)
	if result.Truncated {
		app.warnf("More matching conversations exist; increase or omit --limit, or narrow the filters.\n")
	}
	app.writeSearchUnsupported(result.Sources)
}

func (app *application) writeSearchIssues(result history.Result, verbose bool) {
	if len(result.Issues) == 0 {
		return
	}
	if !verbose && len(result.Issues) > maxSummarizedIssues {
		records := 0
		for _, issue := range result.Issues {
			if issue.Record {
				records++
			}
		}
		if files := len(result.Issues) - records; files > 0 {
			app.warnf("warning: %d histories could not be read (use --verbose to view)\n", files)
		}
		if records > 0 {
			app.warnf("warning: skipped %d malformed history records (use --verbose to view)\n", records)
		}
	} else {
		for _, issue := range result.Issues {
			app.warnf("warning: %s %s: %s\n", issue.Source.Harness, sanitizeHumanText(issue.Path), sanitizeHumanText(issue.Message))
		}
	}
	if result.OmittedIssues > 0 {
		app.warnf("warning: %d more history warnings omitted\n", result.OmittedIssues)
	}
}

func (app *application) writeSearchUnsupported(sources []history.SourceStatus) {
	var unsupported []string
	for _, source := range sources {
		if source.Status != "unsupported" {
			continue
		}
		if name := string(source.Source.Harness); !slices.Contains(unsupported, name) {
			unsupported = append(unsupported, name)
		}
	}
	if len(unsupported) > 0 {
		app.warnf("History readers unavailable: %s.\n", strings.Join(unsupported, ", "))
	}
}

// searchTitle prefers the harness's native title and falls back to the first
// prompt, then the session ID.
func searchTitle(c history.Conversation) string {
	for _, title := range []string{c.Title, c.Prompt} {
		if cleaned := sanitizeHumanText(title); cleaned != "" {
			return cleaned
		}
	}
	return c.SessionID
}

// resumeLine renders a native resume command to paste into a shell, run from
// the conversation's working directory.
func resumeLine(match history.Match) string {
	if len(match.ResumeCommand) == 0 {
		return ""
	}
	quoted := make([]string, len(match.ResumeCommand))
	for i, arg := range match.ResumeCommand {
		quoted[i] = shellQuote(arg)
	}
	command := strings.Join(quoted, " ")
	if match.Conversation.CWD == "" {
		return command
	}
	return "cd " + shellQuote(match.Conversation.CWD) + " && " + command
}

// shellQuote quotes one POSIX shell word, leaving plain words readable.
func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_./:=@%+,", r)
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (app *application) writeSearchField(match history.Match, field string) error {
	var value string
	switch field {
	case "id":
		value = match.Conversation.SessionID
	case "path":
		value = match.Conversation.Path
	case "resume":
		value = resumeLine(match)
	}
	if value == "" {
		return nil
	}
	return app.writeln(sanitizeHumanText(value))
}

func (app *application) writeSearchMatch(match history.Match) error {
	c := match.Conversation
	if err := app.writef("%s %s [%s]\n", c.Harness, sanitizeHumanText(c.SessionID), searchPresence(match.RegistryStates)); err != nil {
		return err
	}
	details := []humanDetail{{label: "Title", value: searchTitle(c)}, {label: "CWD", value: c.CWD}}
	for _, detail := range []humanDetail{{label: "Branch", value: c.GitBranch}, {label: "Model", value: c.Model}} {
		if detail.value != "" {
			details = append(details, detail)
		}
	}
	details = append(details, humanDetail{label: "History", value: c.Path})
	if !c.UpdatedAt.IsZero() {
		details = append(details, humanDetail{label: "Updated", value: c.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	if resume := resumeLine(match); resume != "" {
		details = append(details, humanDetail{label: "Resume", value: resume, wrap: wrapHumanIdentifier})
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

func (app *application) writeSearchMatchTTY(match history.Match, verbose bool) error {
	c := match.Conversation
	width := app.maxLineWidth()
	var right strings.Builder
	if searchPresence(match.RegistryStates) == "live (last observed)" {
		right.WriteString("  \x1b[1;32m● live" + styleReset)
	}
	if !c.UpdatedAt.IsZero() {
		right.WriteString("  \x1b[2m" + formatUpdatedAt(c.UpdatedAt, time.Now().UTC(), false) + styleReset)
	}
	prefix := fmt.Sprintf("\x1b[2m%-*s%s", harnessBadgeWidth, c.Harness, styleReset)
	used := harnessBadgeWidth + humanColumnGap + text.StringWidth(right.String())
	title := truncateHumanText(searchTitle(c), max(minRuleWidth, width-used))
	if err := app.writef("%s  \x1b[1m%s%s%s\n", prefix, title, styleReset, right.String()); err != nil {
		return err
	}
	meta := []string{sanitizeHumanText(c.SessionID)}
	for _, value := range []string{formatHumanPath(c.CWD), c.GitBranch, c.Model} {
		if value != "" {
			meta = append(meta, sanitizeHumanText(value))
		}
	}
	if err := app.writef("  \x1b[2m%s%s\n", truncateHumanText(strings.Join(meta, " · "), max(minRuleWidth, width-metaIndentWidth)), styleReset); err != nil {
		return err
	}
	if verbose {
		if err := app.writef("  \x1b[2mhistory: %s%s\n", sanitizeHumanText(c.Path), styleReset); err != nil {
			return err
		}
	}
	if err := app.writeSearchExcerptsTTY(match.Excerpts, width); err != nil {
		return err
	}
	if resume := resumeLine(match); resume != "" {
		if err := app.writef("  \x1b[2mresume:%s %s\n", styleReset, sanitizeHumanText(resume)); err != nil {
			return err
		}
	}
	return app.writeln()
}

func (app *application) writeSearchExcerptsTTY(excerpts []history.Excerpt, width int) error {
	const branchWidth = 3
	prefixWidth := branchWidth + maxRoleLabelWidth + 1
	textWidth := max(minRuleWidth, width-prefixWidth)
	for i, excerpt := range excerpts {
		branch := "\x1b[2m├─" + styleReset + " "
		continuation := "\x1b[2m│" + styleReset + strings.Repeat(" ", prefixWidth-1)
		if i == len(excerpts)-1 {
			branch = "\x1b[2m└─" + styleReset + " "
			continuation = strings.Repeat(" ", prefixWidth)
		}
		role := excerpt.Role + ":"
		styled := role
		switch excerpt.Role {
		case "user":
			styled = "\x1b[1;33m" + role + styleReset
		case "assistant":
			styled = "\x1b[1;37m" + role + styleReset
		case "tool":
			styled = "\x1b[2m" + role + styleReset
		}
		first := branch + styled + strings.Repeat(" ", max(1, maxRoleLabelWidth-len(role)+1))
		for index, line := range highlightedLines(excerpt.Text, excerpt.Spans, textWidth) {
			lead := continuation
			if index == 0 {
				lead = first
			}
			if err := app.writef("%s%s\n", lead, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// highlightedLines sanitizes excerpt text like sanitizeHumanText, marks the
// match spans, and wraps it at spaces to width display cells.
func highlightedLines(value string, spans []history.Span, width int) []string {
	lines := wrapCells(excerptCells(value, spans), width)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func excerptCells(value string, spans []history.Span) []excerptCell {
	cells := make([]excerptCell, 0, len(value))
	span := 0
	for offset, character := range value {
		for span < len(spans) && spans[span].End <= offset {
			span++
		}
		matched := span < len(spans) && spans[span].Start <= offset
		if blankCharacter(character) {
			character = ' '
		}
		if character == ' ' && (len(cells) == 0 || cells[len(cells)-1].character == ' ') {
			continue
		}
		cells = append(cells, excerptCell{character: character, width: text.StringWidth(string(character)), match: matched})
	}
	for len(cells) > 0 && cells[len(cells)-1].character == ' ' {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func blankCharacter(character rune) bool {
	return unicode.IsControl(character) || isBidiControl(character) || unicode.IsSpace(character)
}

func wrapCells(cells []excerptCell, width int) []string {
	var lines []string
	for len(cells) > 0 {
		end := lineBreak(cells, width)
		lines = append(lines, renderCells(cells[:end]))
		cells = cells[end:]
		for len(cells) > 0 && cells[0].character == ' ' {
			cells = cells[1:]
		}
	}
	return lines
}

func lineBreak(cells []excerptCell, width int) int {
	end, used, space := 0, 0, -1
	for end < len(cells) && (used+cells[end].width <= width || end == 0) {
		if cells[end].character == ' ' {
			space = end
		}
		used += cells[end].width
		end++
	}
	if end < len(cells) && space > 0 {
		return space
	}
	return end
}

func renderCells(cells []excerptCell) string {
	var line strings.Builder
	highlighted := false
	for _, cell := range cells {
		if cell.match != highlighted {
			if cell.match {
				line.WriteString(highlightStart)
			} else {
				line.WriteString(styleReset)
			}
			highlighted = cell.match
		}
		line.WriteRune(cell.character)
	}
	if highlighted {
		line.WriteString(styleReset)
	}
	return line.String()
}

func (app *application) writeSearchList(matches []history.Match) error {
	if len(matches) == 0 {
		return app.writeln("No conversations.")
	}
	now := time.Now().UTC()
	rows := make([][]string, 0, len(matches))
	for _, match := range matches {
		c := match.Conversation
		title := searchTitle(c)
		if searchPresence(match.RegistryStates) == "live (last observed)" {
			title = "● " + title
		}
		rows = append(rows, []string{string(c.Harness), sanitizeHumanText(c.SessionID), formatUpdatedAt(c.UpdatedAt, now, false), strconv.Itoa(c.Messages), title, formatHumanPath(c.CWD)})
	}
	return app.writeHumanTable(searchListColumns(rows, app.maxLineWidth()), rows)
}

// searchListColumns keeps identifiers whole so they can be copied and shares
// the remaining width between the title and the working directory.
func searchListColumns(rows [][]string, maxWidth int) []humanColumn {
	headings := []string{"Agent", "Session", "Updated", "Msgs", "Title", "CWD"}
	widths := make([]int, len(headings))
	for i, heading := range headings {
		widths[i] = text.StringWidth(heading)
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], text.StringWidth(cell))
		}
	}
	fixed := widths[0] + widths[1] + widths[2] + widths[3] + (len(headings)-1)*humanColumnGap
	available := max(minListColumnsWidth, maxWidth-fixed)
	title := min(widths[4], max(minRuleWidth, available*searchTitleShare/searchShareTotal))
	directory := min(widths[5], max(minRuleWidth, available-title))
	title = min(widths[4], max(title, available-directory))
	widths[4], widths[5] = title, directory
	columns := make([]humanColumn, len(headings))
	for i, heading := range headings {
		columns[i] = humanColumn{heading: heading, width: widths[i], wrap: nil, align: text.AlignLeft}
	}
	columns[3].align = text.AlignRight
	columns[5].wrap = wrapHumanPath
	return columns
}

func (app *application) writeSearchGroups(matches []history.Match, groupBy string) error {
	counts := map[string]*searchGroup{}
	for _, match := range matches {
		c := match.Conversation
		var key string
		switch groupBy {
		case "project":
			key = formatHumanPath(cmp.Or(c.ProjectRoot, c.CWD))
		case "harness":
			key = string(c.Harness)
		case "day":
			if !c.UpdatedAt.IsZero() {
				key = c.UpdatedAt.Local().Format(time.DateOnly) //nolint:gosmopolitan // --group-by day buckets by the user's local calendar day
			}
		}
		key = cmp.Or(key, "-")
		if counts[key] == nil {
			counts[key] = &searchGroup{Group: key, Conversations: 0, Messages: 0}
		}
		counts[key].Conversations++
		counts[key].Messages += c.Messages
	}
	groups := make([]searchGroup, 0, len(counts))
	for _, group := range counts {
		groups = append(groups, *group)
	}
	slices.SortFunc(groups, func(a, b searchGroup) int {
		if groupBy == "day" {
			return cmp.Compare(b.Group, a.Group)
		}
		return cmp.Or(cmp.Compare(b.Conversations, a.Conversations), cmp.Compare(a.Group, b.Group))
	})
	if app.outputJSON {
		return app.writeJSONSafe(groups)
	}
	rows := make([][]string, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, []string{sanitizeHumanText(group.Group), strconv.Itoa(group.Conversations), strconv.Itoa(group.Messages)})
	}
	heading := strings.ToUpper(groupBy[:1]) + groupBy[1:]
	columns := []humanColumn{
		{heading: heading, width: 0, wrap: wrapHumanPath, align: text.AlignLeft},
		{heading: "Conversations", width: len("Conversations"), wrap: nil, align: text.AlignRight},
		{heading: "Messages", width: len("Messages"), wrap: nil, align: text.AlignRight},
	}
	columns[0].width = len(heading)
	for _, row := range rows {
		columns[0].width = max(columns[0].width, text.StringWidth(row[0]))
		columns[1].width = max(columns[1].width, len(row[1]))
		columns[2].width = max(columns[2].width, len(row[2]))
	}
	columns[0].width = min(columns[0].width, max(minRuleWidth, app.maxLineWidth()-columns[1].width-columns[2].width-2*humanColumnGap))
	return app.writeWrappedHumanTable(columns, rows)
}

func (app *application) writeSearchJSON(result history.Result) error {
	return app.writeJSONSafe(result)
}

func (app *application) writeSearchJSONLine(match history.Match) error {
	data, err := json.Marshal(match)
	if err != nil {
		return fmt.Errorf("encode history match: %w", err)
	}
	return app.writeln(escapeBidi(string(data)))
}

// writeJSONSafe writes indented JSON with bidirectional controls escaped so a
// terminal cannot reorder transcript text.
func (app *application) writeJSONSafe(value any) error {
	data, err := json.MarshalIndent(value, "", jsonIndent)
	if err != nil {
		return fmt.Errorf("encode history results: %w", err)
	}
	return app.writeln(escapeBidi(string(data)))
}

func escapeBidi(data string) string {
	var safe strings.Builder
	safe.Grow(len(data))
	for _, character := range data {
		if isBidiControl(character) {
			quoted := strconv.QuoteRuneToASCII(character)
			safe.WriteString(quoted[1 : len(quoted)-1])
		} else {
			safe.WriteRune(character)
		}
	}
	return safe.String()
}

func searchPresence(evidence []history.RegistryState) string {
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
