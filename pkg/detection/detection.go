// Package detection evaluates saved terminal screens using AHT's activity rules.
// It performs no process discovery, live screen capture, or registry mutation.
package detection

import (
	"context"
	"errors"
	"fmt"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/pkg/registry"
)

// MaxScreenBytes is the maximum accepted screen fixture size.
const MaxScreenBytes = agentstate.MaxScreenBytes

var (
	// ErrScreenTooLarge indicates a screen fixture exceeds MaxScreenBytes.
	ErrScreenTooLarge = agentstate.ErrScreenTooLarge
	// ErrManifestOptions indicates conflicting explicit and ambient manifest sources.
	ErrManifestOptions = errors.New("manifest path and config directory cannot be used together")
)

// Options selects the screen title, rule source, and optional screen output.
type Options struct {
	Title         string
	ManifestPath  string
	ConfigDir     string
	IncludeScreen bool
}

// Inspection explains the effective decision and every candidate rule.
// Screen is omitted unless explicitly requested through Options.IncludeScreen.
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

// Decision records the first matching rule in manifest priority order.
type Decision struct {
	Activity        registry.Activity `json:"activity"`
	Reason          string            `json:"reason"`
	RuleID          string            `json:"rule_id,omitempty"`
	ManifestSource  string            `json:"manifest_source"`
	ManifestVersion int               `json:"manifest_version"`
	Warning         string            `json:"warning,omitempty"`
	Evidence        []RuleEvidence    `json:"evidence"`
}

// RuleEvidence records a rule considered before or at the winning rule.
type RuleEvidence struct {
	RuleID  string `json:"rule_id"`
	Matched bool   `json:"matched"`
}

// CandidateRule describes a rule's matching and priority outcome.
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

// MatcherInspection explains one configured matcher without copying screen content.
type MatcherInspection struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// Inspect evaluates a bounded screen using the same normalization and matching
// rules as live tracking. Only the last 100 normalized lines are evaluated.
// An explicit manifest fails on invalid input; ambient overrides retain the
// tracker's bundled fallback policy. Custom harness names require an explicit manifest.
func Inspect(ctx context.Context, harness registry.Harness, screen string, options Options) (Inspection, error) {
	var empty Inspection
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("inspect screen: %w", err)
	}
	if len(screen) > MaxScreenBytes {
		return empty, ErrScreenTooLarge
	}
	if options.ManifestPath != "" && options.ConfigDir != "" {
		return empty, ErrManifestOptions
	}
	var manifest agentstate.Manifest
	var err error
	if options.ManifestPath != "" {
		manifest, err = agentstate.LoadExplicitManifest(options.ManifestPath, harness)
	} else {
		manifest, err = (agentstate.Loader{ConfigDir: options.ConfigDir}).Load(harness)
	}
	if err != nil {
		return empty, fmt.Errorf("load detection manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("inspect screen: %w", err)
	}
	raw := manifest.Inspect(agentstate.NormalizeSnapshot(screen, options.Title))
	result := inspectionFromRaw(raw)
	if options.IncludeScreen {
		result.Screen = screen
	}
	return result, nil
}

func inspectionFromRaw(raw agentstate.Inspection) Inspection {
	decision := raw.Decision
	result := Inspection{
		Harness: raw.Harness, ManifestSource: raw.ManifestSource, ManifestVersion: raw.ManifestVersion,
		Warning: raw.Warning, LinesEvaluated: raw.LinesEvaluated, Title: raw.Title,
		Decision: Decision{
			Activity: decision.Activity, Reason: decision.Reason, RuleID: decision.RuleID,
			ManifestSource: decision.ManifestSource, ManifestVersion: decision.ManifestVersion,
			Warning: decision.Warning, Evidence: make([]RuleEvidence, 0, len(decision.Evidence)),
		},
		WinningRule: raw.WinningRule, Candidates: make([]CandidateRule, 0, len(raw.Candidates)), Screen: "",
	}
	for _, ev := range decision.Evidence {
		result.Decision.Evidence = append(result.Decision.Evidence, RuleEvidence(ev))
	}
	for _, rule := range raw.Candidates {
		candidate := CandidateRule{
			ID: rule.ID, State: rule.State, Priority: rule.Priority, Region: rule.Region,
			Matched: rule.Matched, Winner: rule.Winner, Reason: rule.Reason, Matchers: make([]MatcherInspection, 0, len(rule.Matchers)),
		}
		for _, matcher := range rule.Matchers {
			candidate.Matchers = append(candidate.Matchers, MatcherInspection(matcher))
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result
}
