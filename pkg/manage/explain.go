package manage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/herdr"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
	"github.com/zigai/aht/pkg/zellij"
)

var (
	// ErrPaneNotLive indicates that a terminal multiplexer pane is not available for inspection.
	ErrPaneNotLive = errors.New("tmux pane is not live")

	// ErrUnsupportedMultiplexer indicates that the session multiplexer kind does not support screen capture.
	ErrUnsupportedMultiplexer = errors.New("unsupported multiplexer kind")
)

type (
	// Explanation represents the activity diagnosis and decision provenance for an agent session.
	Explanation struct {
		SessionID         string                     `json:"session_id"`
		Harness           registry.Harness           `json:"harness"`
		PaneID            string                     `json:"pane_id,omitempty"`
		Process           *registry.ProcessIdentity  `json:"process,omitempty"`
		ProcessMatch      string                     `json:"process_match"`
		SelectedAuthority string                     `json:"selected_authority"`
		FallbackReason    string                     `json:"fallback_reason,omitempty"`
		FinalActivity     string                     `json:"final_activity"`
		Hook              HookExplanation            `json:"hook"`
		Screen            ScreenExplanation          `json:"screen"`
		RegistryActivity  *registry.Activity         `json:"registry_activity"`
		RegistryDecision  *registry.ActivityDecision `json:"registry_decision,omitempty"`
	}

	// HookExplanation describes the evaluation of native integration hook evidence for a session.
	HookExplanation struct {
		Event           string    `json:"event,omitempty"`
		Integration     string    `json:"integration,omitempty"`
		ObservedAt      time.Time `json:"observed_at,omitzero"`
		Age             string    `json:"age,omitempty"`
		ProcessMatches  bool      `json:"process_matches"`
		Fresh           bool      `json:"fresh"`
		FreshnessReason string    `json:"freshness_reason"`
		Active          bool      `json:"active"`
	}

	// ScreenExplanation describes the evaluation of screen detection heuristics for a session.
	ScreenExplanation struct {
		Evaluated         bool           `json:"evaluated"`
		UnavailableReason string         `json:"unavailable_reason,omitempty"`
		Decision          ScreenDecision `json:"decision"`
		Error             string         `json:"error,omitempty"`
	}

	// ScreenDecision represents the outcome of evaluating a screen state detection rule.
	ScreenDecision struct {
		Activity        registry.Activity `json:"activity"`
		Reason          string            `json:"reason"`
		RuleID          string            `json:"rule_id,omitempty"`
		ManifestSource  string            `json:"manifest_source"`
		ManifestVersion int               `json:"manifest_version"`
		Warning         string            `json:"warning,omitempty"`
		Evidence        []RuleEvidence    `json:"evidence,omitempty"`
	}

	// RuleEvidence records whether an individual detection rule matched during screen evaluation.
	RuleEvidence struct {
		RuleID  string `json:"rule_id"`
		Matched bool   `json:"matched"`
	}

	// ExplainOptions controls how session explanation is evaluated.
	ExplainOptions struct {
		// Now is the reference time used for freshness and lease calculations.
		// If zero, the current UTC time is used.
		Now time.Time

		// LiveScreen enables optional live terminal multiplexer screen capture
		// and manifest evaluation. When false (default), Explain is strictly
		// read-only and explains the stored decision without spawning screen
		// captures or touching terminal panes.
		LiveScreen bool

		// ConfigDir is an optional directory containing override detection manifests.
		ConfigDir string
	}
)

// ExplainSession returns an Explanation for the given session.
// When options.LiveScreen is false, ExplainSession is strictly read-only: it inspects
// the persisted session state, evidence, and stored decision provenance without
// capturing screens or modifying registry state.
// When options.LiveScreen is true and authority is "screen", it additionally performs
// live terminal multiplexer screen capture and manifest re-evaluation.
func ExplainSession(ctx context.Context, session registry.Session, options ExplainOptions) (Explanation, error) {
	if err := ctx.Err(); err != nil {
		var empty Explanation
		return empty, fmt.Errorf("explain session: %w", err)
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	policy := agentstate.PolicyFor(session.Harness)
	hookEvaluation := agentstate.EvaluateHook(session, now)
	authority := string(policy.Primary)
	fallbackReason := ""
	if policy.Primary == agentstate.AuthorityHook && policy.ScreenFallback && !hookEvaluation.Active {
		authority = string(agentstate.AuthorityScreen)
		fallbackReason = hookEvaluation.Reason
	}

	result := buildBaseExplanation(session, now, authority, fallbackReason, hookEvaluation)
	populateStoredScreenDecision(&result, session)

	if !options.LiveScreen {
		if session.ActivityDecision != nil {
			result.SelectedAuthority = session.ActivityDecision.Authority
			result.FallbackReason = ""
		}
		if strings.TrimSpace(session.Multiplexer.PaneID) == "" {
			result.Screen.UnavailableReason = "no_live_pane"
		} else {
			result.Screen.UnavailableReason = "screen_inspection_disabled"
		}
		return result, nil
	}

	screen, finalActivity, screenErr := evaluateExplanationScreen(ctx, session, options.ConfigDir, authority, result.FinalActivity)
	result.Screen = screen
	result.FinalActivity = finalActivity
	if screenErr != nil {
		return result, fmt.Errorf("evaluate live screen: %w", screenErr)
	}
	return result, nil
}

// Explain returns an Explanation for the session identified by sessionID.
func (m *Manager) Explain(ctx context.Context, sessionID string, options ExplainOptions) (Explanation, error) {
	storePath := m.config.StorePath
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}
	store := broker.NewStore(storePath)
	session, err := store.Get(ctx, sessionID)
	if err != nil {
		return Explanation{
			SessionID:         "",
			Harness:           "",
			PaneID:            "",
			Process:           nil,
			ProcessMatch:      "",
			SelectedAuthority: "",
			FallbackReason:    "",
			FinalActivity:     "",
			Hook:              HookExplanation{Event: "", Integration: "", ObservedAt: time.Time{}, Age: "", ProcessMatches: false, Fresh: false, FreshnessReason: "", Active: false},
			Screen:            ScreenExplanation{Evaluated: false, UnavailableReason: "", Decision: ScreenDecision{Activity: registry.ActivityUnknown, Reason: "", RuleID: "", ManifestSource: "", ManifestVersion: 0, Warning: "", Evidence: nil}, Error: ""},
			RegistryActivity:  nil,
			RegistryDecision:  nil,
		}, fmt.Errorf("get session for explanation: %w", err)
	}
	return ExplainSession(ctx, session, options)
}

func buildBaseExplanation(
	session registry.Session,
	now time.Time,
	authority string,
	fallbackReason string,
	hookEvaluation agentstate.HookEvaluation,
) Explanation {
	result := Explanation{
		SessionID:         session.ID,
		Harness:           session.Harness,
		PaneID:            session.Multiplexer.PaneID,
		Process:           cloneProcessIdentity(session.Process),
		ProcessMatch:      processMatchExplanation(session),
		SelectedAuthority: authority,
		FallbackReason:    fallbackReason,
		FinalActivity:     activityString(session.Activity),
		Hook: HookExplanation{
			Event:           "",
			Integration:     "",
			ObservedAt:      time.Time{},
			Age:             "",
			ProcessMatches:  hookEvaluation.ProcessMatches,
			Fresh:           hookEvaluation.Fresh,
			FreshnessReason: hookEvaluation.Reason,
			Active:          hookEvaluation.Active,
		},
		Screen: ScreenExplanation{
			Evaluated:         false,
			UnavailableReason: "",
			Decision: ScreenDecision{
				Activity:        registry.ActivityUnknown,
				Reason:          "",
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				Warning:         "",
				Evidence:        nil,
			},
			Error: "",
		},
		RegistryActivity: cloneActivity(session.Activity),
		RegistryDecision: cloneActivityDecision(session.ActivityDecision),
	}

	if native := session.Observations.Native; native != nil {
		result.Hook.Event = native.Event
		result.Hook.Integration = native.Attributes["aht_integration"]
		result.Hook.ObservedAt = native.ObservedAt
		result.Hook.Age = now.Sub(native.ObservedAt).Round(time.Millisecond).String()
	}

	return result
}

func populateStoredScreenDecision(result *Explanation, session registry.Session) {
	if session.ActivityDecision != nil && session.ActivityDecision.Authority == string(agentstate.AuthorityScreen) {
		activity := registry.ActivityUnknown
		if session.Activity != nil {
			activity = *session.Activity
		}
		result.Screen.Decision = ScreenDecision{
			Activity:        activity,
			Reason:          session.ActivityDecision.Reason,
			RuleID:          session.ActivityDecision.RuleID,
			ManifestSource:  session.ActivityDecision.ManifestSource,
			ManifestVersion: session.ActivityDecision.ManifestVersion,
			Warning:         "",
			Evidence:        nil,
		}
		return
	}
	if session.Observations.Screen != nil {
		result.Screen.Decision = ScreenDecision{
			Activity:        session.Observations.Screen.Activity,
			Reason:          session.Observations.Screen.Reason,
			RuleID:          session.Observations.Screen.RuleID,
			ManifestSource:  session.Observations.Screen.ManifestSource,
			ManifestVersion: session.Observations.Screen.ManifestVersion,
			Warning:         "",
			Evidence:        nil,
		}
	}
}

func evaluateExplanationScreen(
	ctx context.Context,
	session registry.Session,
	configDir string,
	authority string,
	currentActivity string,
) (ScreenExplanation, string, error) {
	if authority != string(agentstate.AuthorityScreen) {
		return ScreenExplanation{
			Evaluated:         false,
			UnavailableReason: "",
			Decision: ScreenDecision{
				Activity:        registry.ActivityUnknown,
				Reason:          "",
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				Warning:         "",
				Evidence:        nil,
			},
			Error: "",
		}, currentActivity, nil
	}
	if strings.TrimSpace(session.Multiplexer.PaneID) == "" {
		return ScreenExplanation{
			Evaluated:         false,
			UnavailableReason: "no_live_pane",
			Decision: ScreenDecision{
				Activity:        registry.ActivityUnknown,
				Reason:          "",
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				Warning:         "",
				Evidence:        nil,
			},
			Error: "",
		}, string(registry.ActivityUnknown), nil
	}
	decision, evaluated, err := evaluateLiveScreen(ctx, session, configDir)
	screen := ScreenExplanation{
		Evaluated:         evaluated,
		UnavailableReason: "",
		Decision:          decision,
		Error:             "",
	}
	if err != nil {
		screen.Error = err.Error()
		return screen, string(registry.ActivityUnknown), err
	}
	if evaluated {
		return screen, string(decision.Activity), nil
	}
	return screen, currentActivity, nil
}

func evaluateLiveScreen(
	ctx context.Context,
	session registry.Session,
	configDir string,
) (ScreenDecision, bool, error) {
	if session.Multiplexer.Kind != registry.MultiplexerTmux {
		return evaluateNonTmuxLiveScreen(ctx, session, configDir)
	}
	return evaluateTmuxLiveScreen(ctx, session, configDir)
}

func evaluateNonTmuxLiveScreen(
	ctx context.Context,
	session registry.Session,
	configDir string,
) (ScreenDecision, bool, error) {
	pane := mux.Pane{
		Location:    session.Multiplexer,
		Processes:   nil,
		ProcessTTY:  "",
		Command:     "",
		CWD:         "",
		Title:       "",
		Activity:    nil,
		StateReason: "",
	}
	var text string
	var title string
	switch session.Multiplexer.Kind {
	case registry.MultiplexerTmux:
		return ScreenDecision{
			Activity:        registry.ActivityUnknown,
			Reason:          "",
			RuleID:          "",
			ManifestSource:  "",
			ManifestVersion: 0,
			Warning:         "",
			Evidence:        nil,
		}, false, fmt.Errorf("%w: %s", ErrPaneNotLive, session.Multiplexer.PaneID)
	case registry.MultiplexerZellij:
		snapshot, err := zellij.CapturePane(ctx, pane)
		if err != nil {
			return ScreenDecision{
				Activity:        registry.ActivityUnknown,
				Reason:          "",
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				Warning:         "",
				Evidence:        nil,
			}, false, fmt.Errorf("capture %s pane: %w", session.Multiplexer.Kind, err)
		}
		text, title = snapshot.Text, snapshot.Title
	case registry.MultiplexerHerdr:
		snapshot, err := herdr.CapturePane(ctx, pane)
		if err != nil {
			return ScreenDecision{
				Activity:        registry.ActivityUnknown,
				Reason:          "",
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				Warning:         "",
				Evidence:        nil,
			}, false, fmt.Errorf("capture %s pane: %w", session.Multiplexer.Kind, err)
		}
		text, title = snapshot.Text, snapshot.Title
	default:
		return ScreenDecision{
			Activity:        registry.ActivityUnknown,
			Reason:          "",
			RuleID:          "",
			ManifestSource:  "",
			ManifestVersion: 0,
			Warning:         "",
			Evidence:        nil,
		}, false, fmt.Errorf("%w: %s", ErrUnsupportedMultiplexer, session.Multiplexer.Kind)
	}
	return evaluateSnapshotText(session.Harness, text, title, configDir)
}

func evaluateTmuxLiveScreen(
	ctx context.Context,
	session registry.Session,
	configDir string,
) (ScreenDecision, bool, error) {
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		return ScreenDecision{
			Activity:        registry.ActivityUnknown,
			Reason:          "",
			RuleID:          "",
			ManifestSource:  "",
			ManifestVersion: 0,
			Warning:         "",
			Evidence:        nil,
		}, false, fmt.Errorf("list tmux panes: %w", err)
	}
	for _, pane := range panes {
		if pane.Tmux.PaneID != session.Tmux.PaneID || !sameTmuxServer(pane.ServerIdentity, session.Tmux.ServerSocket) {
			continue
		}
		snapshot, captureErr := tmux.CapturePane(ctx, pane)
		if captureErr != nil {
			return ScreenDecision{
				Activity:        registry.ActivityUnknown,
				Reason:          "",
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				Warning:         "",
				Evidence:        nil,
			}, false, fmt.Errorf("capture tmux pane: %w", captureErr)
		}
		return evaluateSnapshotText(session.Harness, snapshot.Text, snapshot.Title, configDir)
	}
	return ScreenDecision{
		Activity:        registry.ActivityUnknown,
		Reason:          "",
		RuleID:          "",
		ManifestSource:  "",
		ManifestVersion: 0,
		Warning:         "",
		Evidence:        nil,
	}, false, fmt.Errorf("%w: %s", ErrPaneNotLive, session.Tmux.PaneID)
}

func evaluateSnapshotText(
	harness registry.Harness,
	text string,
	title string,
	configDir string,
) (ScreenDecision, bool, error) {
	loader := agentstate.Loader{ConfigDir: configDir}
	manifest, err := loader.Load(harness)
	if err != nil {
		return ScreenDecision{
			Activity:        registry.ActivityUnknown,
			Reason:          "",
			RuleID:          "",
			ManifestSource:  "",
			ManifestVersion: 0,
			Warning:         "",
			Evidence:        nil,
		}, false, fmt.Errorf("load detection manifest: %w", err)
	}
	rawDecision := manifest.Evaluate(agentstate.NormalizeSnapshot(text, title))
	evidence := make([]RuleEvidence, 0, len(rawDecision.Evidence))
	for _, ev := range rawDecision.Evidence {
		evidence = append(evidence, RuleEvidence{
			RuleID:  ev.RuleID,
			Matched: ev.Matched,
		})
	}
	decision := ScreenDecision{
		Activity:        rawDecision.Activity,
		Reason:          rawDecision.Reason,
		RuleID:          rawDecision.RuleID,
		ManifestSource:  rawDecision.ManifestSource,
		ManifestVersion: rawDecision.ManifestVersion,
		Warning:         rawDecision.Warning,
		Evidence:        evidence,
	}
	return decision, true, nil
}

func processMatchExplanation(session registry.Session) string {
	if session.Process == nil {
		return "unavailable"
	}
	if session.Process.Foreground && session.Process.TTY != "" && session.Process.TTY == session.Multiplexer.PaneTTY {
		return "foreground_tty_process"
	}
	if session.Observations.Multiplexer != nil && session.Observations.Multiplexer.Process.Equal(*session.Process) {
		return "pid_start_identity"
	}
	if session.Observations.Tmux != nil && session.Observations.Tmux.Process.Equal(*session.Process) {
		return "pid_start_identity"
	}
	return "unverified"
}

func sameTmuxServer(left string, right string) bool {
	if left == "" {
		left = "default"
	}
	if right == "" {
		right = "default"
	}
	return left == right
}

func activityString(activity *registry.Activity) string {
	if activity == nil {
		return string(registry.ActivityUnknown)
	}
	return string(*activity)
}

func cloneProcessIdentity(p *registry.ProcessIdentity) *registry.ProcessIdentity {
	if p == nil {
		return nil
	}
	val := *p
	return &val
}

func cloneActivity(a *registry.Activity) *registry.Activity {
	if a == nil {
		return nil
	}
	val := *a
	return &val
}

func cloneActivityDecision(d *registry.ActivityDecision) *registry.ActivityDecision {
	if d == nil {
		return nil
	}
	val := *d
	return &val
}
