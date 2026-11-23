package aht

import (
	"context"
	"time"

	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	// ModeAuto routes through the broker socket and falls back to disk if offline.
	ModeAuto Mode = client.ModeAuto

	// ModeRealtimeOnly connects strictly to the broker socket, failing if offline.
	ModeRealtimeOnly Mode = client.ModeRealtimeOnly

	// ModeDurableOnly reads directly from the on-disk registry file.
	ModeDurableOnly Mode = client.ModeDurableOnly

	PresenceLive Presence = registry.PresenceLive

	PresenceGone Presence = registry.PresenceGone

	PresenceUnknown Presence = registry.PresenceUnknown

	ActivityRunning Activity = registry.ActivityRunning

	// ActivityWaiting indicates the agent is waiting for user or tool input.
	ActivityWaiting Activity = registry.ActivityWaiting

	ActivityIdle Activity = registry.ActivityIdle

	ActivityFailed Activity = registry.ActivityFailed

	ActivityInterrupted Activity = registry.ActivityInterrupted

	ActivityUnknown Activity = registry.ActivityUnknown

	MultiplexerTmux                  MultiplexerKind = registry.MultiplexerTmux
	MultiplexerZellij                MultiplexerKind = registry.MultiplexerZellij
	MultiplexerHerdr                 MultiplexerKind = registry.MultiplexerHerdr
	SummaryGroupByMultiplexerSession SummaryGroupBy  = registry.SummaryGroupByMultiplexerSession
	SummaryGroupByProject            SummaryGroupBy  = registry.SummaryGroupByProject
	SummaryGroupByHarness            SummaryGroupBy  = registry.SummaryGroupByHarness

	HarnessLocationKindConfig       HarnessLocationKind  = harness.LocationKindConfig
	HarnessLocationKindInstructions HarnessLocationKind  = harness.LocationKindInstructions
	HarnessLocationKindSkills       HarnessLocationKind  = harness.LocationKindSkills
	HarnessLocationKindCommands     HarnessLocationKind  = harness.LocationKindCommands
	HarnessLocationKindMCP          HarnessLocationKind  = harness.LocationKindMCP
	HarnessLocationScopeGlobal      HarnessLocationScope = harness.LocationScopeGlobal
	HarnessLocationScopeProject     HarnessLocationScope = harness.LocationScopeProject

	ArtifactMissing  ArtifactState = manage.ArtifactMissing
	ArtifactCurrent  ArtifactState = manage.ArtifactCurrent
	ArtifactStale    ArtifactState = manage.ArtifactStale
	ArtifactForeign  ArtifactState = manage.ArtifactForeign
	ArtifactPartial  ArtifactState = manage.ArtifactPartial
	ArtifactDisabled ArtifactState = manage.ArtifactDisabled

	HealthStatusHealthy  HealthStatus = manage.HealthStatusHealthy
	HealthStatusDegraded HealthStatus = manage.HealthStatusDegraded
	HealthStatusStale    HealthStatus = manage.HealthStatusStale
	HealthStatusMissing  HealthStatus = manage.HealthStatusMissing
	HealthStatusCorrupt  HealthStatus = manage.HealthStatusCorrupt

	DoctorStatusOK   DoctorStatus = manage.DoctorStatusOK
	DoctorStatusInfo DoctorStatus = manage.DoctorStatusInfo
	DoctorStatusWarn DoctorStatus = manage.DoctorStatusWarn
	DoctorStatusFail DoctorStatus = manage.DoctorStatusFail
	DoctorStatusSkip DoctorStatus = manage.DoctorStatusSkip
)

const (
	DoctorActionRefreshHistory     = manage.DoctorActionRefreshHistory
	DoctorActionResetStore         = manage.DoctorActionResetStore
	DoctorActionEnableTracker      = manage.DoctorActionEnableTracker
	DoctorActionInspectTracker     = manage.DoctorActionInspectTracker
	DoctorActionRemoveHealth       = manage.DoctorActionRemoveHealth
	DoctorActionReplaceIntegration = manage.DoctorActionReplaceIntegration
	DoctorActionUpgrade            = manage.DoctorActionUpgrade
	DoctorActionUpgradeIntegration = manage.DoctorActionUpgradeIntegration
	DoctorActionInitConfig         = manage.DoctorActionInitConfig
)

var (
	ErrInvalidMode = client.ErrInvalidMode

	ErrUnavailable = broker.ErrUnavailable

	// ErrBusy means the realtime broker refused a connection because all slots are occupied.
	ErrBusy = broker.ErrBusy

	ErrStreamClosed = broker.ErrStreamClosed

	ErrProtocol = broker.ErrProtocol

	ErrRealtimeRequired = client.ErrRealtimeRequired

	ErrAmbiguousSession = client.ErrAmbiguousSession

	ErrSessionNotFound = registry.ErrSessionNotFound

	ErrNoCurrentSession   = client.ErrNoCurrentSession
	ErrUnsupportedGroupBy = registry.ErrUnsupportedGroupBy
	ErrWaitTimeout        = client.ErrWaitTimeout

	ErrSessionDisappeared = client.ErrSessionDisappeared

	ErrUnknownState = client.ErrUnknownState

	// ErrConditionRequired means neither activity nor presence condition was specified.
	ErrConditionRequired = client.ErrConditionRequired

	ErrContradictoryCondition = client.ErrContradictoryCondition

	// ErrInvalidDuration means a duration was negative or stable-for reached timeout.
	ErrInvalidDuration = client.ErrInvalidDuration

	// ErrSessionRequired means no session reference was given: a registry ID,
	// registry ID prefix, native session ID, or session path.
	ErrSessionRequired = client.ErrSessionRequired
	// ErrObservationConflict means an incoming observation contradicts or precedes accepted evidence.
	ErrObservationConflict = registry.ErrObservationConflict

	ErrInvalidObservation = registry.ErrInvalidObservation

	ErrCorruptStore = registry.ErrCorruptStore

	ErrStoreTooLarge = registry.ErrStoreTooLarge

	ErrHarnessRequired = registry.ErrHarnessRequired

	ErrObservationIdentity = registry.ErrObservationIdentity

	ErrUnknownHarness = registry.ErrUnknownHarness

	ErrUnknownPresence = registry.ErrUnknownPresence

	ErrUnknownActivity = registry.ErrUnknownActivity

	ErrHealthMissing = manage.ErrHealthMissing

	ErrHealthStale = manage.ErrHealthStale

	ErrHealthDegraded = manage.ErrHealthDegraded

	ErrHealthCorrupt = manage.ErrHealthCorrupt

	ErrReconciliationIncomplete = manage.ErrReconciliationIncomplete

	ErrPaneNotLive = manage.ErrPaneNotLive

	// ErrForeignTracker means the tracker service definition is not owned by AHT.
	ErrForeignTracker = manage.ErrForeignTracker

	ErrConfig = manage.ErrConfig

	// ErrNoRepair means a doctor check has no automatic repair.
	ErrNoRepair = manage.ErrNoRepair
)

type (
	Client = client.Client

	Config = client.Config

	// Mode controls how a Client routes operations between the realtime broker
	// and the durable registry file on disk.
	Mode = client.Mode

	Session = registry.Session

	Filter = registry.Filter

	StateSnapshot = registry.StateSnapshot

	Presence = registry.Presence

	Activity = registry.Activity

	Harness = registry.Harness

	Liveness = registry.Liveness
	Live     = registry.Live
	Gone     = registry.Gone
	Unknown  = registry.Unknown

	Location = registry.Location

	MultiplexerKind = registry.MultiplexerKind

	Selector = client.Selector

	SessionLister = client.SessionLister

	CurrentOptions = client.CurrentOptions

	AmbiguousSessionError = client.AmbiguousSessionError

	Observation = registry.Observation

	ObservationIdentity = registry.ObservationIdentity

	// Summary represents aggregate session counts for a terminal session.
	Summary = registry.Summary

	SummaryGroupBy = registry.SummaryGroupBy

	SummaryOptions = registry.SummaryOptions

	GroupedSummarizer = registry.GroupedSummarizer

	// Subscription streams immutable state snapshots from the realtime broker.
	Subscription = broker.Subscription

	Store = registry.Store

	WaitOptions = client.WaitOptions

	WaitResult  = client.WaitResult
	Explanation = manage.Explanation

	HookExplanation = manage.HookExplanation

	ScreenExplanation = manage.ScreenExplanation

	ScreenDecision = manage.ScreenDecision

	RuleEvidence = manage.RuleEvidence

	ExplainOptions = manage.ExplainOptions

	TrackerHealth = manage.TrackerHealth

	ObserverHealth = manage.ObserverHealth

	HealthStatus = manage.HealthStatus

	DoctorStatus = manage.DoctorStatus

	DoctorCheck = manage.DoctorCheck

	// DoctorFix says how to resolve a warn or fail doctor check.
	DoctorFix    = manage.DoctorFix
	DoctorAction = manage.DoctorAction

	DoctorSummary = manage.DoctorSummary

	// DoctorResult is the complete diagnostic report, in check order.
	DoctorResult = manage.DoctorResult

	DoctorOptions = manage.DoctorOptions

	HarnessCapabilities = harness.Capabilities

	// HarnessLocation is a path where a harness documents reading user-facing configuration.
	HarnessLocation = harness.Location

	HarnessLocationKind = harness.LocationKind

	HarnessLocationScope = harness.LocationScope

	// Manager installs harness integrations and controls the platform-native AHT tracker.
	Manager = manage.Manager

	ManagerConfig = manage.Config

	IntegrationOptions = manage.IntegrationOptions

	UpgradeOptions = manage.UpgradeOptions

	IntegrationResult = manage.IntegrationResult

	IntegrationStatus = manage.IntegrationStatus

	// ArtifactStatus describes the ownership and freshness of a managed integration artifact.
	ArtifactStatus = manage.ArtifactStatus

	ArtifactState = manage.ArtifactState

	TrackerOptions = manage.TrackerOptions

	TrackerResult = manage.TrackerResult
)

type (
	Evidence        = registry.Evidence
	EvidenceKind    = registry.EvidenceKind
	Verification    = registry.Verification
	Report          = registry.Report
	Reporter        = registry.Reporter
	Sighting        = registry.Sighting
	Placement       = registry.Placement
	Listing         = registry.Listing
	Reading         = registry.Reading
	Incarnation     = registry.Incarnation
	Authority       = registry.Authority
	Lifecycle       = registry.NativeLifecycle
	ProcessIdentity = registry.ProcessIdentity
)

func New(config Config) *Client {
	return client.New(config)
}

func NewManager(config ManagerConfig) *Manager {
	return manage.New(config)
}

func DefaultSocketPath() string {
	return broker.DefaultSocketPath()
}

func DefaultStorePath() string {
	return registry.DefaultStorePath()
}

// NewSubscription creates a subscription wrapping custom channels.
func NewSubscription(
	snapshots <-chan StateSnapshot,
	errCh <-chan error,
	cancel context.CancelFunc,
) *Subscription {
	return broker.NewSubscription(snapshots, errCh, cancel)
}

func IsUnavailable(err error) bool {
	return broker.IsUnavailable(err)
}

func StreamClosed(err error) bool {
	return broker.StreamClosed(err)
}

func Resolve(ctx context.Context, lister SessionLister, selector Selector) (Session, error) {
	return client.Resolve(ctx, lister, selector)
}

func ResolveSessions(sessions []Session, selector Selector) (Session, error) {
	return client.ResolveSessions(sessions, selector)
}

func Current(ctx context.Context, c *Client) (Session, error) {
	return c.Current(ctx)
}

func ExplainSession(
	ctx context.Context,
	session Session,
	opts ExplainOptions,
) (Explanation, error) {
	return manage.ExplainSession(ctx, session, opts)
}

// ReadTrackerHealth reads and evaluates the tracker health sidecar at path.
func ReadTrackerHealth(path string, now time.Time, maxAge time.Duration) (TrackerHealth, error) {
	return manage.ReadTrackerHealth(path, now, maxAge)
}

func Capabilities(harnessID Harness) (HarnessCapabilities, bool) {
	return harness.CapabilitiesFor(harnessID)
}

func AllCapabilities() []HarnessCapabilities {
	return harness.AllCapabilities()
}

// HarnessLocations returns the documented configuration locations of harnessID:
// settings, instructions, skills, commands, and MCP files, for the current user
// and for projectDir. Project locations are omitted when projectDir is empty.
// It returns false for an unknown harness.
func HarnessLocations(harnessID Harness, projectDir string) ([]HarnessLocation, bool) {
	return harness.LocationsFor(harnessID, projectDir)
}

func AllHarnessLocations(projectDir string) []HarnessLocation {
	return harness.AllLocations(projectDir)
}
