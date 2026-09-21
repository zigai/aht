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

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/history"
	"github.com/zigai/aht/pkg/registry"
)

const (
	harnessBadgeWidth      = 7
	maxRuleWidth           = 80
	minRuleWidth           = 20
	maxSummarizedIssues    = 3
	maxDisplayIDLen        = 16
	maxRoleLabelWidth      = 12
	needleHighlightPadding = 16
)

var (
	errSearchIncomplete    = errors.New("search incomplete; some histories could not be searched")
	errSearchSource        = errors.New("invalid --source")
	errSearchAgentConflict = errors.New("conflicting --agent and --source harnesses")
	errSearchArgumentCount = errors.New("expected exactly one text argument")
	errSearchRole          = errors.New("invalid --role")
)

type searchOptions struct {
	query   history.Query
	agent   string
	role    string
	sources []string
	verbose bool
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
	flags.BoolVarP(&options.verbose, "verbose", "v", false, "show detailed file paths and all diagnostic warnings")
	flags.StringVar(&options.role, "role", "", "search only messages from this role `<user|agent|all>` (default: all)")
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
	if err := options.validateRole(); err != nil {
		return nil, err
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
		return fmt.Errorf("search: %w %q; choose user, agent, or all", errSearchRole, options.role)
	}
	return nil
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
	} else if err := app.writeSearchResults(result, searchErr, options); err != nil {
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

func (app *application) writeSearchResults(result history.Result, searchErr error, options searchOptions) error {
	if len(result.Matches) == 0 {
		empty := "No matching conversations."
		if searchErr != nil {
			empty = "No matching conversations; some sources could not be searched."
		}
		if err := app.writeln(empty); err != nil {
			return err
		}
	}
	isTTY := app.isColorEnabled()
	for _, match := range result.Matches {
		var err error
		if isTTY {
			err = app.writeSearchMatchTTY(match, options.query, options.verbose)
		} else {
			err = app.writeSearchMatch(match)
		}
		if err != nil {
			return err
		}
	}
	if isTTY && len(result.Matches) > 0 {
		ruleWidth := min(maxRuleWidth, max(minRuleWidth, app.maxLineWidth()))
		if err := app.writeln("\x1b[2m" + strings.Repeat("─", ruleWidth) + "\x1b[0m"); err != nil {
			return err
		}
	}
	app.writeSearchIssues(result.Issues, options.verbose)
	if result.Truncated {
		app.warnf("More matching conversations exist; increase or omit --limit, or narrow --agent/--dir.\n")
	}
	app.writeSearchUnsupported(result.Sources)
	return nil
}

func (app *application) writeSearchIssues(issues []history.Issue, verbose bool) {
	if len(issues) == 0 {
		return
	}
	if !verbose && len(issues) > maxSummarizedIssues {
		app.warnf("warning: %d historical files had read warnings (use --verbose to view)\n", len(issues))
		return
	}
	for _, issue := range issues {
		app.warnf("warning: %s %s: %s\n", issue.Source.Harness, sanitizeHumanText(issue.Path), sanitizeHumanText(issue.Message))
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

func (app *application) writeSearchMatchTTY(match history.Match, query history.Query, verbose bool) error {
	c := match.Conversation
	dispID := c.SessionID
	if len(dispID) > maxDisplayIDLen {
		dispID = dispID[:registryIDShortLength]
	}
	prefix := fmt.Sprintf("\x1b[2m%-7s  %s\x1b[0m", c.Harness, dispID)
	var presence string
	if searchPresence(match.Live) == "live (last observed)" {
		presence = "  \x1b[1;32m● live\x1b[0m"
	}
	var cwd string
	if c.CWD != "" {
		cwd = fmt.Sprintf("  \x1b[2m%s\x1b[0m", formatHumanPath(c.CWD))
	}
	var age string
	if !c.UpdatedAt.IsZero() {
		age = fmt.Sprintf("  \x1b[2m(%s)\x1b[0m", formatUpdatedAt(c.UpdatedAt, time.Now().UTC(), false))
	}
	title := resolveSearchTitle(c, match.Excerpts)
	rightText := presence + cwd + age
	maxLineWidth := app.maxLineWidth()
	fixedWidth := harnessBadgeWidth + humanColumnGap + len(dispID) + text.StringWidth(sanitizeHumanText(rightText)) + humanColumnGap
	availTitle := max(minRuleWidth, maxLineWidth-fixedWidth)
	title = truncateHumanText(title, availTitle)
	if err := app.writef("%s  \x1b[1m%s\x1b[0m%s\n", prefix, title, rightText); err != nil {
		return err
	}
	if verbose {
		if err := app.writef("  \x1b[2mhistory: %s\x1b[0m\n", c.Path); err != nil {
			return err
		}
	}
	if err := app.writeSearchExcerptsTTY(match.Excerpts, query); err != nil {
		return err
	}
	return app.writeln()
}

func (app *application) writeSearchExcerptsTTY(excerpts []history.Excerpt, query history.Query) error {
	if len(excerpts) == 0 {
		return nil
	}
	maxWidth := app.maxLineWidth()
	const branchWidth = 3
	prefixWidth := branchWidth + maxRoleLabelWidth + 1
	textWidth := max(minRuleWidth, maxWidth-prefixWidth)
	for i, excerpt := range excerpts {
		branch := "\x1b[2m├─\x1b[0m "
		continuationPrefix := "\x1b[2m│\x1b[0m" + strings.Repeat(" ", prefixWidth-1)
		if i == len(excerpts)-1 {
			branch = "\x1b[2m└─\x1b[0m "
			continuationPrefix = strings.Repeat(" ", prefixWidth)
		}
		role := excerpt.Role + ":"
		roleStyled := role
		switch excerpt.Role {
		case "user":
			roleStyled = "\x1b[1;33muser:\x1b[0m"
		case "assistant":
			roleStyled = "\x1b[1;37massistant:\x1b[0m"
		case "tool":
			roleStyled = "\x1b[2mtool:\x1b[0m"
		}
		padding := strings.Repeat(" ", max(1, maxRoleLabelWidth-len(role)+1))
		firstLinePrefix := branch + roleStyled + padding
		cleanText := sanitizeHumanText(excerpt.Text)
		lines := wrapHumanText(cleanText, textWidth)
		for lineIdx, line := range lines {
			highlighted := highlightNeedle(line, query.Text, query.CaseSensitive)
			if lineIdx == 0 {
				if err := app.writef("%s%s\n", firstLinePrefix, highlighted); err != nil {
					return err
				}
			} else {
				if err := app.writef("%s%s\n", continuationPrefix, highlighted); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func stripBoilerplateTags(title string) string {
	for _, tag := range []string{"</INSTRUCTIONS>", "</environment_context>", "</context>", "</attachment>"} {
		if _, after, ok := strings.Cut(title, tag); ok {
			if trimmed := strings.TrimSpace(after); trimmed != "" {
				title = trimmed
			}
		}
	}
	for _, tag := range []string{"<INSTRUCTIONS>", "<environment_context>", "<context>", "<attachment>", "<skill"} {
		if _, after, ok := strings.Cut(title, tag); ok {
			if endTag := strings.IndexByte(after, '>'); endTag >= 0 {
				after = after[endTag+1:]
			}
			if trimmed := strings.TrimSpace(after); trimmed != "" {
				title = trimmed
			}
		}
	}
	return title
}

func stripPathPrefix(title string) string {
	if _, after, ok := strings.Cut(title, " <"); ok {
		return strings.TrimSpace(after)
	}
	idx := strings.Index(title, " ")
	if idx >= 0 {
		if next := strings.Index(title[idx+1:], " "); next >= 0 {
			return strings.TrimSpace(title[idx+1+next+1:])
		}
	}
	return title
}

func cleanSessionTitle(title string) string {
	title = sanitizeHumanText(title)
	title = stripBoilerplateTags(title)
	for _, prefix := range []string{"# AGENTS.md instructions", "AGENTS.md instructions", "# INSTRUCTIONS"} {
		if idx := strings.Index(title, prefix); idx >= 0 {
			title = strings.TrimSpace(title[idx+len(prefix):])
		}
	}
	if strings.HasPrefix(title, "for /") {
		title = stripPathPrefix(title)
	}
	for _, clipped := range []string{"<INSTRUC", "<environ", "<attach"} {
		if idx := strings.LastIndex(title, clipped); idx >= 0 {
			title = strings.TrimSpace(title[:idx])
		}
	}
	return strings.TrimLeft(title, " …:#-><\t\r\n")
}

func isMeaningfulTitle(title string) bool {
	const minMeaningfulLen = 16
	const absoluteMinLen = 4
	if len(title) < minMeaningfulLen {
		if strings.HasSuffix(title, "…") || strings.HasSuffix(title, "...") {
			return false
		}
		if len(title) < absoluteMinLen {
			return false
		}
	}
	return !isBoilerplateTitle(title)
}

func isBoilerplateTitle(title string) bool {
	return strings.HasPrefix(title, "<") ||
		strings.HasPrefix(title, "for /") ||
		strings.HasPrefix(title, "/") ||
		strings.Contains(title, "AGENTS.md") ||
		strings.Contains(title, "</cwd>") ||
		strings.Contains(title, "<cwd>") ||
		strings.Contains(title, "approval_policy")
}

func findMeaningfulExcerpt(excerpts []history.Excerpt) string {
	for _, role := range []string{"user", "assistant"} {
		for _, exc := range excerpts {
			if exc.Role == role {
				if candidate := cleanSessionTitle(exc.Text); isMeaningfulTitle(candidate) {
					return candidate
				}
			}
		}
	}
	return ""
}

func resolveSearchTitle(c history.Conversation, excerpts []history.Excerpt) string {
	title := cleanSessionTitle(c.Title)
	if isMeaningfulTitle(title) {
		return title
	}
	if candidate := findMeaningfulExcerpt(excerpts); candidate != "" {
		return candidate
	}
	if title != "" {
		return title
	}
	return c.SessionID
}

func highlightNeedle(s, needle string, caseSensitive bool) string {
	if needle == "" || len(s) < len(needle) {
		return s
	}
	target := s
	searchTarget := target
	searchNeedle := needle
	if !caseSensitive {
		searchTarget = strings.ToLower(target)
		searchNeedle = strings.ToLower(needle)
	}
	var b strings.Builder
	b.Grow(len(s) + needleHighlightPadding)
	for {
		idx := strings.Index(searchTarget, searchNeedle)
		if idx < 0 {
			b.WriteString(target)
			break
		}
		b.WriteString(target[:idx])
		b.WriteString("\x1b[1;36m")
		b.WriteString(target[idx : idx+len(needle)])
		b.WriteString("\x1b[0m")
		target = target[idx+len(needle):]
		searchTarget = searchTarget[idx+len(needle):]
	}
	return b.String()
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
