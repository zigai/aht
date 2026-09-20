package aht

import (
	"context"
	"fmt"
	"time"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/manage"
	"github.com/zigai/aht/pkg/registry"
)

const (
	// ModeAuto routes through the broker socket and falls back to disk if offline.
	ModeAuto Mode = client.ModeAuto

	// ModeRealtimeOnly connects strictly to the broker socket, failing if offline.
	ModeRealtimeOnly Mode = client.ModeRealtimeOnly

	// ModeDurableOnly reads directly from the on-disk registry file.
	ModeDurableOnly Mode = client.ModeDurableOnly

	// PresenceLive indicates the agent session process or container is active.
	PresenceLive Presence = registry.PresenceLive

	// PresenceGone indicates the agent session has terminated.
	PresenceGone Presence = registry.PresenceGone

	// PresenceUnknown indicates presence cannot be determined.
	PresenceUnknown Presence = registry.PresenceUnknown

	// ActivityRunning indicates the agent is actively processing or executing.
	ActivityRunning Activity = registry.ActivityRunning

	// ActivityWaiting indicates the agent is waiting for user or tool input.
	ActivityWaiting Activity = registry.ActivityWaiting

	// ActivityIdle indicates the agent is idle and ready for interaction.
	ActivityIdle Activity = registry.ActivityIdle

	// ActivityFailed indicates the agent encountered an unrecoverable failure.
	ActivityFailed Activity = registry.ActivityFailed

	// ActivityInterrupted indicates the agent run was canceled or interrupted.
	ActivityInterrupted Activity = registry.ActivityInterrupted

	// ActivityUnknown indicates activity cannot be determined.
	ActivityUnknown Activity = registry.ActivityUnknown

	HarnessClaude   Harness = registry.HarnessClaude
	HarnessCodex    Harness = registry.HarnessCodex
	HarnessCursor   Harness = registry.HarnessCursor
	HarnessCopilot  Harness = registry.HarnessCopilot
	HarnessCline    Harness = registry.HarnessCline
	HarnessKimiCode Harness = registry.HarnessKimiCode
	HarnessGrok     Harness = registry.HarnessGrok
	HarnessGoose    Harness = registry.HarnessGoose
	HarnessPi       Harness = registry.HarnessPi
	HarnessOmp      Harness = registry.HarnessOmp
	HarnessOpenCode Harness = registry.HarnessOpenCode
	HarnessAgy      Harness = registry.HarnessAgy
	HarnessKilo     Harness = registry.HarnessKilo
	HarnessDroid    Harness = registry.HarnessDroid
	HarnessOpenClaw Harness = registry.HarnessOpenClaw
	HarnessHermes   Harness = registry.HarnessHermes

	MultiplexerTmux                  MultiplexerKind = client.MultiplexerTmux
	MultiplexerZellij                MultiplexerKind = client.MultiplexerZellij
	MultiplexerHerdr                 MultiplexerKind = client.MultiplexerHerdr
	SummaryGroupByMultiplexerSession SummaryGroupBy  = registry.SummaryGroupByMultiplexerSession
	SummaryGroupByProject            SummaryGroupBy  = registry.SummaryGroupByProject
	SummaryGroupByHarness            SummaryGroupBy  = registry.SummaryGroupByHarness
)

var (
	// ErrInvalidMode means the client was configured with an unsupported operating mode.
	ErrInvalidMode = client.ErrInvalidMode

	// ErrUnavailable means no realtime AHT broker accepted the local connection.
	ErrUnavailable = client.ErrUnavailable

	// ErrProtocol means the broker returned an invalid or incompatible response.
	ErrProtocol = client.ErrProtocol

	// ErrRealtimeRequired means the operation requires a realtime broker connection.
	ErrRealtimeRequired = client.ErrRealtimeRequired

	// ErrAmbiguousSession indicates that a selector or reference matched multiple sessions.
	ErrAmbiguousSession = client.ErrAmbiguousSession

	// ErrSessionNotFound is returned when no session matches the selector.
	ErrSessionNotFound = client.ErrSessionNotFound

	// ErrNoCurrentSession indicates that no agent session was found for the current context.
	ErrNoCurrentSession = client.ErrNoCurrentSession
	// ErrUnsupportedGroupBy means an unsupported summary grouping was requested.
	ErrUnsupportedGroupBy = registry.ErrUnsupportedGroupBy
	// ErrWaitTimeout means the wait operation timed out before the condition was satisfied.
	ErrWaitTimeout = client.ErrWaitTimeout

	// ErrSessionDisappeared means the session departed or became gone before the requested condition was met.
	ErrSessionDisappeared = client.ErrSessionDisappeared

	// ErrUnknownState means the session entered an indeterminate or unknown state.
	ErrUnknownState = client.ErrUnknownState

	// ErrConditionRequired means neither activity nor presence condition was specified.
	ErrConditionRequired = client.ErrConditionRequired

	// ErrContradictoryCondition means contradictory wait conditions were specified.
	ErrContradictoryCondition = client.ErrContradictoryCondition

	// ErrInvalidDuration means a duration was negative or stable-for exceeded timeout.
	ErrInvalidDuration = client.ErrInvalidDuration

	// ErrSessionRequired means a canonical session ID was not provided.
	ErrSessionRequired = client.ErrSessionRequired
	// ErrObservationConflict means an incoming observation contradicts or precedes accepted evidence.
	ErrObservationConflict = registry.ErrObservationConflict

	// ErrInvalidObservation means an observation failed structural validation.
	ErrInvalidObservation = registry.ErrInvalidObservation

	// ErrCorruptStore means registry persistence contained unreadable or malformed state.
	ErrCorruptStore = registry.ErrCorruptStore

	// ErrStoreTooLarge means registry state exceeds the configured maximum size.
	ErrStoreTooLarge = registry.ErrStoreTooLarge

	// ErrHarnessRequired means an observation omitted the required harness identifier.
	ErrHarnessRequired = registry.ErrHarnessRequired

	// ErrObservationIdentity means an observation omitted the required identity fields.
	ErrObservationIdentity = registry.ErrObservationIdentity

	// ErrUnknownHarness means an invalid harness name was encountered.
	ErrUnknownHarness = registry.ErrUnknownHarness

	// ErrUnknownPresence means an invalid presence value was encountered.
	ErrUnknownPresence = registry.ErrUnknownPresence

	// ErrUnknownActivity means an invalid activity value was encountered.
	ErrUnknownActivity = registry.ErrUnknownActivity

	// ErrHealthMissing means the background tracker health sidecar file is missing.
	ErrHealthMissing = manage.ErrHealthMissing

	// ErrHealthStale means the background tracker has not reconciled within the expected window.
	ErrHealthStale = manage.ErrHealthStale

	// ErrHealthDegraded means the background tracker reported a degraded state.
	ErrHealthDegraded = manage.ErrHealthDegraded

	// ErrHealthCorrupt means the background tracker health sidecar file contains invalid JSON.
	ErrHealthCorrupt = manage.ErrHealthCorrupt

	// ErrReconciliationIncomplete means the observer has not completed any successful reconciliation.
	ErrReconciliationIncomplete = manage.ErrReconciliationIncomplete

	// ErrPaneNotLive means the target terminal multiplexer pane is not running or available.
	ErrPaneNotLive = manage.ErrPaneNotLive
)

type (
	// Client reads and updates agent-harness state through the local AHT broker.
	Client = client.Client

	// Config identifies the local AHT instance and operating mode used by a Client.
	Config = client.Config

	// Mode controls how a Client routes operations between the realtime broker
	// and the durable registry file on disk.
	Mode = client.Mode

	// Session represents an agent-harness session tracked by AHT.
	Session = registry.Session

	// Filter specifies matching criteria when querying or watching sessions.
	Filter = registry.Filter

	// StateSnapshot is a revisioned collection of tracked sessions.
	StateSnapshot = registry.StateSnapshot

	// Presence indicates whether an agent session is live, gone, or unknown.
	Presence = registry.Presence

	// Activity indicates what an agent is currently doing.
	Activity = registry.Activity

	// Harness identifies a supported AI coding agent.
	Harness = registry.Harness

	// TmuxContext represents the tmux multiplexer location of a session.
	TmuxContext = registry.TmuxContext

	// MultiplexerContext represents the unified multiplexer location of a session.
	MultiplexerContext = registry.MultiplexerContext

	// MultiplexerKind identifies a supported terminal multiplexer.
	MultiplexerKind = client.MultiplexerKind

	// Selector specifies criteria for selecting and resolving a single session.
	Selector = client.Selector

	// SessionLister captures the session query capability required to resolve selectors.
	SessionLister = client.SessionLister

	// CurrentContextOptions identifies the process whose enclosing session is requested.
	CurrentContextOptions = client.CurrentContextOptions

	// AmbiguousSessionError provides detailed information about ambiguous session matches.
	AmbiguousSessionError = client.AmbiguousSessionError

	// Observation represents an observation recorded for a session.
	Observation = registry.Observation

	// ObservationIdentity identifies the session an observation belongs to.
	ObservationIdentity = registry.ObservationIdentity

	// Summary represents aggregate session counts for a terminal session.
	Summary = registry.Summary

	// SummaryGroupBy represents the grouping dimension for aggregate session summaries.
	SummaryGroupBy = registry.SummaryGroupBy

	// SummaryOptions configures grouping for aggregate session summaries.
	SummaryOptions = registry.SummaryOptions

	// GroupedSummarizer extends a Store with configurable grouping options.
	GroupedSummarizer = registry.GroupedSummarizer

	// Subscription streams immutable state snapshots from the realtime broker.
	Subscription = broker.Subscription

	// Store represents an engine that persists or serves agent-harness state.
	Store = registry.Store

	// WaitOptions configures the session condition, timeout, and stability duration to wait for.
	WaitOptions = client.WaitOptions

	// WaitResult describes the session state that satisfied the wait condition.
	WaitResult = client.WaitResult
	// Explanation represents the activity diagnosis and decision provenance for an agent session.
	Explanation = manage.Explanation

	// HookExplanation describes the evaluation of native integration hook evidence.
	HookExplanation = manage.HookExplanation

	// ScreenExplanation describes the evaluation of screen detection heuristics.
	ScreenExplanation = manage.ScreenExplanation

	// ScreenDecision represents the outcome of evaluating a screen state detection rule.
	ScreenDecision = manage.ScreenDecision

	// RuleEvidence records whether an individual detection rule matched during screen evaluation.
	RuleEvidence = manage.RuleEvidence

	// ExplainOptions controls how session explanation is evaluated.
	ExplainOptions = manage.ExplainOptions

	// TrackerHealth represents background tracker reconciliation health and freshness.
	TrackerHealth = manage.TrackerHealth

	// HealthStatus indicates the operational state of tracker reconciliation.
	HealthStatus = manage.HealthStatus

	// DoctorStatus represents the severity level of a doctor diagnostic check.
	DoctorStatus = manage.DoctorStatus

	// DoctorCheck represents one diagnostic check evaluated by Doctor.
	DoctorCheck = manage.DoctorCheck

	// DoctorResult represents the complete diagnostic status of the AHT installation.
	DoctorResult = manage.DoctorResult

	// DoctorOptions controls what checks and details Doctor evaluates.
	DoctorOptions = manage.DoctorOptions

	// HarnessCapabilities describes the static capabilities and supported features of an agent harness.
	HarnessCapabilities = harness.Capabilities

	// HarnessRuntimeStatus describes the installed/runtime state of a harness integration.
	HarnessRuntimeStatus = harness.RuntimeStatus

	// Manager installs harness integrations and controls the platform-native AHT tracker.
	Manager = manage.Manager

	// ManagerConfig identifies the binary, registry, and tracker settings used by a Manager.
	ManagerConfig = manage.Config
)

// New returns a client for the configured local AHT instance.
func New(config Config) *Client {
	return client.New(config)
}

// DefaultSocketPath returns the default endpoint for the current user's AHT broker.
func DefaultSocketPath() string {
	return broker.DefaultSocketPath()
}

// DefaultStorePath returns the default filesystem location for the durable registry.
func DefaultStorePath() string {
	return registry.DefaultStorePath()
}

// NewSubscription creates a subscription wrapping custom channels.
func NewSubscription(snapshots <-chan StateSnapshot, errors <-chan error, cancel context.CancelFunc) *Subscription {
	return broker.NewSubscription(snapshots, errors, cancel)
}

// IsUnavailable reports whether err means that no realtime broker accepted the connection.
func IsUnavailable(err error) bool {
	return client.IsUnavailable(err)
}

// Resolve finds a single session matching selector using the provided session lister.
func Resolve(ctx context.Context, lister SessionLister, selector Selector) (Session, error) {
	return client.Resolve(ctx, lister, selector) //nolint:wrapcheck // facade forwards client error unchanged
}

// ResolveSessions resolves a selector against an existing session snapshot.
func ResolveSessions(sessions []Session, selector Selector) (Session, error) {
	session, err := client.ResolveSessions(sessions, selector)
	if err != nil {
		return session, fmt.Errorf("resolve session: %w", err)
	}
	return session, nil
}

// Current returns the session for the calling agent context using the provided client.
func Current(ctx context.Context, c *Client) (Session, error) {
	return c.Current(ctx) //nolint:wrapcheck // facade forwards client error unchanged
}

// ExplainSession returns an Explanation for the given session.
func ExplainSession(ctx context.Context, session Session, options ExplainOptions) (Explanation, error) {
	exp, err := manage.ExplainSession(ctx, session, options)
	if err != nil {
		return exp, fmt.Errorf("explain session: %w", err)
	}
	return exp, nil
}

// ReadTrackerHealth reads and evaluates the tracker health sidecar at path.
func ReadTrackerHealth(path string, now time.Time, maxAge time.Duration) (TrackerHealth, error) {
	health, err := manage.ReadTrackerHealth(path, now, maxAge)
	if err != nil {
		return health, fmt.Errorf("read tracker health: %w", err)
	}
	return health, nil
}

// Capabilities returns the static capabilities of harnessID.
func Capabilities(harnessID Harness) (HarnessCapabilities, bool) {
	return harness.CapabilitiesFor(harnessID)
}

// AllCapabilities returns the static capabilities of all supported harnesses.
func AllCapabilities() []HarnessCapabilities {
	return harness.AllCapabilities()
}
