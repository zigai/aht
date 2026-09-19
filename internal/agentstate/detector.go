package agentstate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

const maxSnapshotLines = 100

var ansiEscapePattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)

type Snapshot struct {
	Lines []string
	Title string
}

type RuleEvidence struct {
	RuleID  string `json:"rule_id"`
	Matched bool   `json:"matched"`
}

type Decision struct {
	Activity        registry.Activity `json:"activity"`
	Reason          string            `json:"reason"`
	RuleID          string            `json:"rule_id,omitempty"`
	ManifestSource  string            `json:"manifest_source"`
	ManifestVersion int               `json:"manifest_version"`
	Warning         string            `json:"warning,omitempty"`
	Evidence        []RuleEvidence    `json:"evidence"`
}

type MatcherInspection struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

type CandidateRule struct {
	ID       string              `json:"id"`
	State    string              `json:"state"`
	Priority int                 `json:"priority"`
	Region   string              `json:"region"`
	Matched  bool                `json:"matched"`
	Winner   bool                `json:"winner"`
	Reason   string              `json:"reason"`
	Matchers []MatcherInspection `json:"matchers,omitempty"`
}

type Inspection struct {
	Harness         registry.Harness `json:"harness"`
	ManifestSource  string           `json:"manifest_source"`
	ManifestVersion int              `json:"manifest_version"`
	Warning         string           `json:"warning,omitempty"`
	LinesEvaluated  int              `json:"lines_evaluated"`
	Title           string           `json:"title,omitempty"`
	Decision        Decision         `json:"decision"`
	WinningRule     string           `json:"winning_rule,omitempty"`
	Candidates      []CandidateRule  `json:"candidates"`
	Screen          string           `json:"screen,omitempty"`
}

func NormalizeSnapshot(text string, title string) Snapshot {
	text = ansiEscapePattern.ReplaceAllString(text, "")
	lines := mux.BoundBottomLines(text, maxSnapshotLines)
	return Snapshot{Lines: lines, Title: ansiEscapePattern.ReplaceAllString(title, "")}
}

func (manifest *Manifest) Evaluate(snapshot Snapshot) Decision {
	decision := Decision{Activity: registry.ActivityUnknown, Reason: "no_rule_matched", RuleID: "", ManifestSource: manifest.Source, ManifestVersion: manifest.Version, Warning: manifest.Warning, Evidence: make([]RuleEvidence, 0, len(manifest.Rules))}
	for _, rule := range sortedRules(manifest.Rules) {
		matched := rule.matches(snapshot)
		decision.Evidence = append(decision.Evidence, RuleEvidence{RuleID: rule.ID, Matched: matched})
		if !matched {
			continue
		}
		activity, _ := registry.NormalizeActivity(rule.State)
		decision.Activity = activity
		decision.Reason = "manifest_rule"
		decision.RuleID = rule.ID
		return decision
	}
	return decision
}

func (manifest *Manifest) Inspect(snapshot Snapshot) Inspection {
	decision := manifest.Evaluate(snapshot)
	sorted := sortedRules(manifest.Rules)

	var winnerPriority int
	for _, rule := range sorted {
		if rule.ID == decision.RuleID {
			winnerPriority = rule.Priority
			break
		}
	}

	candidates := make([]CandidateRule, 0, len(sorted))
	for _, rule := range sorted {
		matched, matchers, firstFailure := rule.inspect(snapshot)
		winner := decision.RuleID != "" && rule.ID == decision.RuleID

		var reason string
		switch {
		case winner:
			reason = "matched"
		case matched:
			if winnerPriority == rule.Priority {
				reason = fmt.Sprintf("matched (shadowed by %q: equal priority %d, earlier in manifest)", decision.RuleID, rule.Priority)
			} else {
				reason = fmt.Sprintf("matched (shadowed by %q: higher priority %d > %d)", decision.RuleID, winnerPriority, rule.Priority)
			}
		default:
			if firstFailure != "" {
				reason = firstFailure
			} else {
				reason = "did not match"
			}
		}

		candidates = append(candidates, CandidateRule{
			ID:       rule.ID,
			State:    rule.State,
			Priority: rule.Priority,
			Region:   rule.Region,
			Matched:  matched,
			Winner:   winner,
			Reason:   reason,
			Matchers: matchers,
		})
	}

	return Inspection{
		Harness:         registry.Harness(manifest.Agent),
		ManifestSource:  manifest.Source,
		ManifestVersion: manifest.Version,
		Warning:         manifest.Warning,
		LinesEvaluated:  len(snapshot.Lines),
		Title:           snapshot.Title,
		Decision:        decision,
		WinningRule:     decision.RuleID,
		Candidates:      candidates,
		Screen:          "",
	}
}

func (rule Rule) inspect(snapshot Snapshot) (bool, []MatcherInspection, string) {
	var matchers []MatcherInspection
	var firstFailure string

	regionLines, regionInspection, regionOK, regionConfigured := inspectRegionMatcher(rule, snapshot.Lines)
	if regionConfigured {
		matchers = append(matchers, regionInspection)
		if !regionOK {
			return false, matchers, "region: " + regionInspection.Details
		}
	}

	text := strings.Join(regionLines, "\n")
	title := snapshot.Title
	if !rule.CaseSensitive {
		text = strings.ToLower(text)
		title = strings.ToLower(title)
	}

	appendMatcher := func(m MatcherInspection, ok bool, fail string) {
		matchers = append(matchers, m)
		if !ok && firstFailure == "" {
			firstFailure = fail
		}
	}
	inspectTextMatchers(rule, text, appendMatcher)
	inspectTitleMatchers(rule, title, appendMatcher)

	matched := rule.matches(snapshot)
	return matched, matchers, firstFailure
}

func inspectRegionMatcher(rule Rule, lines []string) ([]string, MatcherInspection, bool, bool) {
	regionLines, regionErr := selectRegion(rule.Region, lines)
	if rule.Region == "" || rule.Region == "all" {
		return regionLines, MatcherInspection{Name: "region", Passed: true, Details: ""}, true, false
	}
	if regionErr != nil {
		return nil, MatcherInspection{
			Name:    "region",
			Passed:  false,
			Details: regionErr.Error(),
		}, false, true
	}
	return regionLines, MatcherInspection{
		Name:    "region",
		Passed:  true,
		Details: fmt.Sprintf("%d lines selected", len(regionLines)),
	}, true, true
}

func inspectTextMatchers(rule Rule, text string, appendMatcher func(MatcherInspection, bool, string)) {
	if len(rule.All) > 0 {
		appendMatcher(inspectAllMatcher(rule, text))
	}
	if len(rule.Any) > 0 {
		appendMatcher(inspectAnyMatcher(rule, text))
	}
	if len(rule.None) > 0 {
		appendMatcher(inspectNoneMatcher(rule, text))
	}
	if len(rule.regexAllCompiled) > 0 {
		appendMatcher(inspectRegexAllMatcher(rule, text))
	}
	if len(rule.regexAnyCompiled) > 0 {
		appendMatcher(inspectRegexAnyMatcher(rule, text))
	}
	if len(rule.regexNoneCompiled) > 0 {
		appendMatcher(inspectRegexNoneMatcher(rule, text))
	}
}

func inspectTitleMatchers(rule Rule, title string, appendMatcher func(MatcherInspection, bool, string)) {
	if len(rule.TitleAny) > 0 {
		appendMatcher(inspectTitleAnyMatcher(rule, title))
	}
	if len(rule.titleRegexAnyCompiled) > 0 {
		appendMatcher(inspectTitleRegexAnyMatcher(rule, title))
	}
}

func inspectAllMatcher(rule Rule, text string) (MatcherInspection, bool, string) {
	var missing []string
	for _, literal := range rule.All {
		if !strings.Contains(text, normalizedLiteral(literal, rule.CaseSensitive)) {
			missing = append(missing, literal)
		}
	}
	if len(missing) == 0 {
		return MatcherInspection{
			Name:    "all",
			Passed:  true,
			Details: fmt.Sprintf("all %d literals matched", len(rule.All)),
		}, true, ""
	}
	failMsg := fmt.Sprintf("missing literal %q", missing[0])
	return MatcherInspection{
		Name:    "all",
		Passed:  false,
		Details: failMsg,
	}, false, "all: " + failMsg
}

func inspectAnyMatcher(rule Rule, text string) (MatcherInspection, bool, string) {
	for _, literal := range rule.Any {
		if strings.Contains(text, normalizedLiteral(literal, rule.CaseSensitive)) {
			return MatcherInspection{
				Name:    "any",
				Passed:  true,
				Details: fmt.Sprintf("matched literal %q", literal),
			}, true, ""
		}
	}
	failMsg := fmt.Sprintf("none of %d literals matched", len(rule.Any))
	return MatcherInspection{
		Name:    "any",
		Passed:  false,
		Details: failMsg,
	}, false, "any: " + failMsg
}

func inspectNoneMatcher(rule Rule, text string) (MatcherInspection, bool, string) {
	for _, literal := range rule.None {
		if strings.Contains(text, normalizedLiteral(literal, rule.CaseSensitive)) {
			failMsg := fmt.Sprintf("matched excluded literal %q", literal)
			return MatcherInspection{
				Name:    "none",
				Passed:  false,
				Details: failMsg,
			}, false, "none: " + failMsg
		}
	}
	return MatcherInspection{
		Name:    "none",
		Passed:  true,
		Details: fmt.Sprintf("none of %d excluded literals matched", len(rule.None)),
	}, true, ""
}

func inspectRegexAllMatcher(rule Rule, text string) (MatcherInspection, bool, string) {
	for i, expr := range rule.regexAllCompiled {
		if !expr.MatchString(text) {
			failMsg := fmt.Sprintf("regular expression %q did not match", rule.RegexAll[i])
			return MatcherInspection{
				Name:    "regex_all",
				Passed:  false,
				Details: failMsg,
			}, false, "regex_all: " + failMsg
		}
	}
	return MatcherInspection{
		Name:    "regex_all",
		Passed:  true,
		Details: fmt.Sprintf("all %d regular expressions matched", len(rule.RegexAll)),
	}, true, ""
}

func inspectRegexAnyMatcher(rule Rule, text string) (MatcherInspection, bool, string) {
	for i, expr := range rule.regexAnyCompiled {
		if expr.MatchString(text) {
			return MatcherInspection{
				Name:    "regex_any",
				Passed:  true,
				Details: fmt.Sprintf("matched regular expression %q", rule.RegexAny[i]),
			}, true, ""
		}
	}
	failMsg := fmt.Sprintf("none of %d regular expressions matched", len(rule.RegexAny))
	return MatcherInspection{
		Name:    "regex_any",
		Passed:  false,
		Details: failMsg,
	}, false, "regex_any: " + failMsg
}

func inspectRegexNoneMatcher(rule Rule, text string) (MatcherInspection, bool, string) {
	for i, expr := range rule.regexNoneCompiled {
		if expr.MatchString(text) {
			failMsg := fmt.Sprintf("matched excluded regular expression %q", rule.RegexNone[i])
			return MatcherInspection{
				Name:    "regex_none",
				Passed:  false,
				Details: failMsg,
			}, false, "regex_none: " + failMsg
		}
	}
	return MatcherInspection{
		Name:    "regex_none",
		Passed:  true,
		Details: fmt.Sprintf("none of %d excluded regular expressions matched", len(rule.RegexNone)),
	}, true, ""
}

func inspectTitleAnyMatcher(rule Rule, title string) (MatcherInspection, bool, string) {
	for _, literal := range rule.TitleAny {
		if strings.Contains(title, normalizedLiteral(literal, rule.CaseSensitive)) {
			return MatcherInspection{
				Name:    "title_any",
				Passed:  true,
				Details: fmt.Sprintf("matched title literal %q", literal),
			}, true, ""
		}
	}
	failMsg := fmt.Sprintf("none of %d title literals matched", len(rule.TitleAny))
	return MatcherInspection{
		Name:    "title_any",
		Passed:  false,
		Details: failMsg,
	}, false, "title_any: " + failMsg
}

func inspectTitleRegexAnyMatcher(rule Rule, title string) (MatcherInspection, bool, string) {
	for i, expr := range rule.titleRegexAnyCompiled {
		if expr.MatchString(title) {
			return MatcherInspection{
				Name:    "title_regex_any",
				Passed:  true,
				Details: fmt.Sprintf("matched title regular expression %q", rule.TitleRegexAny[i]),
			}, true, ""
		}
	}
	failMsg := fmt.Sprintf("none of %d title regular expressions matched", len(rule.TitleRegexAny))
	return MatcherInspection{
		Name:    "title_regex_any",
		Passed:  false,
		Details: failMsg,
	}, false, "title_regex_any: " + failMsg
}

func (rule Rule) matches(snapshot Snapshot) bool {
	region, err := selectRegion(rule.Region, snapshot.Lines)
	if err != nil {
		return false
	}
	text := strings.Join(region, "\n")
	title := snapshot.Title
	if !rule.CaseSensitive {
		text = strings.ToLower(text)
		title = strings.ToLower(title)
	}
	return rule.matchesText(text) && rule.matchesTitle(title)
}

func (rule Rule) matchesText(text string) bool {
	for _, literal := range rule.All {
		if !strings.Contains(text, normalizedLiteral(literal, rule.CaseSensitive)) {
			return false
		}
	}
	if len(rule.Any) > 0 && !containsAny(text, rule.Any, rule.CaseSensitive) {
		return false
	}
	if containsAny(text, rule.None, rule.CaseSensitive) || !matchesAllRegex(text, rule.regexAllCompiled) {
		return false
	}
	if len(rule.regexAnyCompiled) > 0 && !matchesAnyRegex(text, rule.regexAnyCompiled) {
		return false
	}
	return !matchesAnyRegex(text, rule.regexNoneCompiled)
}

func (rule Rule) matchesTitle(title string) bool {
	if len(rule.TitleAny) > 0 && !containsAny(title, rule.TitleAny, rule.CaseSensitive) {
		return false
	}
	return len(rule.titleRegexAnyCompiled) == 0 || matchesAnyRegex(title, rule.titleRegexAnyCompiled)
}

func normalizedLiteral(value string, caseSensitive bool) string {
	if caseSensitive {
		return value
	}
	return strings.ToLower(value)
}

func containsAny(text string, values []string, caseSensitive bool) bool {
	for _, value := range values {
		if strings.Contains(text, normalizedLiteral(value, caseSensitive)) {
			return true
		}
	}
	return false
}

func matchesAllRegex(text string, expressions []*regexp.Regexp) bool {
	for _, expression := range expressions {
		if !expression.MatchString(text) {
			return false
		}
	}

	return true
}

func ruleRegexExpression(expression string, caseSensitive bool) string {
	if caseSensitive {
		return expression
	}
	return "(?i:" + expression + ")"
}

func matchesAnyRegex(text string, expressions []*regexp.Regexp) bool {
	for _, expression := range expressions {
		if expression.MatchString(text) {
			return true
		}
	}

	return false
}
