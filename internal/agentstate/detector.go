package agentstate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

const maxSnapshotLines = 100

const (
	// ReasonManifestRule marks a decision taken by a manifest rule.
	ReasonManifestRule = "manifest_rule"
	// ReasonNoRuleMatched marks a decision where no manifest rule matched.
	ReasonNoRuleMatched = "no_rule_matched"
)

// CSI parameter, intermediate, and final bytes use their standard ASCII ranges.
var ansiEscapePattern = regexp.MustCompile(
	`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\a\x1b]*(?:\a|\x1b\\)|)`,
)

// ErrManifestOptions reports conflicting explicit and ambient manifest sources.
var ErrManifestOptions = errors.New("manifest path and config directory cannot be used together")

type Snapshot struct {
	Lines []string
	Title string
}

type RuleEvidence struct {
	RuleID  string `json:"rule_id"`
	Matched bool   `json:"matched"`
}

type Decision struct {
	Activity        registry.Activity       `json:"activity"`
	Detail          registry.ActivityDetail `json:"detail,omitempty"`
	Reason          string                  `json:"reason"`
	RuleID          string                  `json:"rule_id,omitempty"`
	ManifestSource  string                  `json:"manifest_source"`
	ManifestVersion int                     `json:"manifest_version"`
	Warning         string                  `json:"warning,omitempty"`
	Evidence        []RuleEvidence          `json:"evidence"`
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

// InspectOptions selects the screen title, rule source, and optional screen output.
//
//exhaustruct:optional
type InspectOptions struct {
	Title         string
	ManifestPath  string
	ConfigDir     string
	IncludeScreen bool
}

type ruleResult struct {
	matched      bool
	matchers     []MatcherInspection
	firstFailure string
}

// InspectScreen evaluates a saved screen with the same normalization and
// matching as live tracking. An explicit manifest fails on invalid input;
// ambient overrides keep the bundled fallback. Custom harness names require
// an explicit manifest.
func InspectScreen(
	ctx context.Context,
	harnessID registry.Harness,
	screen string,
	opts InspectOptions,
) (Inspection, error) {
	var empty Inspection
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("inspect screen: %w", err)
	}

	if len(screen) > MaxScreenBytes {
		return empty, ErrScreenTooLarge
	}

	if opts.ManifestPath != "" && opts.ConfigDir != "" {
		return empty, ErrManifestOptions
	}

	var (
		manifest Manifest
		err      error
	)
	if opts.ManifestPath != "" {
		manifest, err = LoadExplicitManifest(opts.ManifestPath, harnessID)
	} else {
		manifest, err = (Loader{ConfigDir: opts.ConfigDir}).Load(harnessID)
	}

	if err != nil {
		return empty, fmt.Errorf("load detection manifest: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("inspect screen: %w", err)
	}

	inspection := manifest.Inspect(NormalizeSnapshot(screen, opts.Title))
	if opts.IncludeScreen {
		inspection.Screen = screen
	}

	return inspection, nil
}

func NormalizeSnapshot(text string, title string) Snapshot {
	text = ansiEscapePattern.ReplaceAllString(text, "")
	lines := mux.BoundBottomLines(text, maxSnapshotLines)

	return Snapshot{Lines: lines, Title: ansiEscapePattern.ReplaceAllString(title, "")}
}

func (manifest *Manifest) Evaluate(snapshot Snapshot) Decision {
	decision, _ := manifest.decide(snapshot, false)

	return decision
}

func (manifest *Manifest) Inspect(snapshot Snapshot) Inspection {
	decision, candidates := manifest.decide(snapshot, true)

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

func (manifest *Manifest) decide(snapshot Snapshot, explain bool) (Decision, []CandidateRule) {
	decision := Decision{
		Activity:        registry.ActivityUnknown,
		Reason:          ReasonNoRuleMatched,
		RuleID:          "",
		ManifestSource:  manifest.Source,
		ManifestVersion: manifest.Version,
		Warning:         manifest.Warning,
		Evidence:        make([]RuleEvidence, 0, len(manifest.Rules)),
		Detail:          "",
	}
	sorted := sortedRules(manifest.Rules)

	var (
		candidates     []CandidateRule
		winnerPriority int
	)
	if explain {
		candidates = make([]CandidateRule, 0, len(sorted))
	}

	for _, rule := range sorted {
		result := rule.evaluate(snapshot, explain)

		winner := result.matched && decision.RuleID == ""
		if decision.RuleID == "" {
			decision.Evidence = append(
				decision.Evidence,
				RuleEvidence{RuleID: rule.ID, Matched: result.matched},
			)
		}

		if winner {
			decision.Activity, _ = registry.NormalizeActivity(rule.State)
			decision.Reason = ReasonManifestRule
			decision.Detail = rule.Detail
			decision.RuleID = rule.ID
			winnerPriority = rule.Priority

			if !explain {
				return decision, nil
			}
		}

		if explain {
			candidates = append(candidates, CandidateRule{
				ID:       rule.ID,
				State:    rule.State,
				Priority: rule.Priority,
				Region:   rule.Region,
				Matched:  result.matched,
				Winner:   winner,
				Reason: candidateReason(
					result,
					winner,
					decision.RuleID,
					winnerPriority,
					rule.Priority,
				),
				Matchers: result.matchers,
			})
		}
	}

	return decision, candidates
}

func candidateReason(
	result ruleResult,
	winner bool,
	winnerID string,
	winnerPriority int,
	priority int,
) string {
	switch {
	case winner:
		return "matched"
	case result.matched && winnerPriority == priority:
		return fmt.Sprintf(
			"matched (shadowed by %q: equal priority %d, earlier in manifest)",
			winnerID,
			priority,
		)
	case result.matched:
		return fmt.Sprintf(
			"matched (shadowed by %q: higher priority %d > %d)",
			winnerID,
			winnerPriority,
			priority,
		)
	case result.firstFailure != "":
		return result.firstFailure
	default:
		return "did not match"
	}
}

func (rule Rule) evaluate(snapshot Snapshot, explain bool) ruleResult {
	var result ruleResult

	region := strings.TrimSpace(rule.Region)

	lines, err := selectRegion(region, snapshot.Lines)
	if region != "" && region != "all" && explain {
		result.matchers = append(result.matchers, regionInspection(lines, err))
	}

	if err != nil {
		if explain {
			result.firstFailure = "region: " + err.Error()
		}

		return result
	}

	text, title := rule.matchTargets(lines, snapshot.Title)
	for _, matcher := range rule.matchers {
		target := text
		if matcher.title {
			target = title
		}

		passed, index := matcher.check(target)
		if !explain {
			if !passed {
				return result
			}

			continue
		}

		details := matcher.describe(index)
		result.matchers = append(
			result.matchers,
			MatcherInspection{Name: matcher.name, Passed: passed, Details: details},
		)

		if !passed && result.firstFailure == "" {
			result.firstFailure = matcher.name + ": " + details
		}
	}

	result.matched = result.firstFailure == ""

	return result
}

func (rule Rule) matchTargets(lines []string, title string) (string, string) {
	text := strings.Join(lines, "\n")
	if rule.CaseSensitive {
		return text, title
	}

	return strings.ToLower(text), strings.ToLower(title)
}

func regionInspection(lines []string, err error) MatcherInspection {
	if err != nil {
		return MatcherInspection{Name: "region", Passed: false, Details: err.Error()}
	}

	return MatcherInspection{
		Name:    "region",
		Passed:  true,
		Details: fmt.Sprintf("%d lines selected", len(lines)),
	}
}
