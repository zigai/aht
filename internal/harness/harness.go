package harness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	EnvSessionID   EnvField = "session_id"
	EnvSessionPath EnvField = "session_path"
	EnvProjectRoot EnvField = "project_root"
	EnvPID         EnvField = "pid"
	EnvEvent       EnvField = "event"
)

// ErrCatalogUnavailable means a native catalog is not running or installed on
// this host. Catalog aggregation omits the provider without degrading results.
var ErrCatalogUnavailable = errors.New("native catalog unavailable")

type (
	EnvField string
)

//exhaustruct:optional
type EnvKeys struct {
	Config      []string
	SessionID   []string
	SessionPath []string
	ProjectRoot []string
	PID         []string
	Event       []string
}

//exhaustruct:optional
type PayloadDefaults struct {
	SessionID   string
	SessionPath string
	CWD         string
	ProjectRoot string
	Event       string
	Attributes  map[string]string
}

//exhaustruct:optional
type Capabilities struct {
	SessionStart      bool
	SessionEnd        bool
	RunningIdle       bool
	WaitingPermission bool
	ProcessIdentity   bool
}

// Definition describes an adapter. LifecycleEventAttribute names the report
// attribute that holds the native event when a report has none, and
// LifecycleSourceAttributes the attributes searched, in order, for the start
// source; when empty, "source" and "reason" are searched.
//
//exhaustruct:optional
type Definition struct {
	ExclusiveProcess          bool
	CatalogCreates            bool
	RetainNativeActivity      bool
	ID                        registry.Harness
	Aliases                   []string
	ProcessNames              []string
	Env                       EnvKeys
	Capabilities              Capabilities
	IntegrationVersion        int
	IntegrationSource         string
	StateAuthority            registry.Authority
	ScreenFallback            bool
	PayloadOmissions          []string
	LifecycleEventAttribute   string
	LifecycleSourceAttributes []string
}

type Adapter interface {
	Definition() Definition
}

// CatalogSession is one session returned by a harness-native catalog. Activity
// describes the provider's current state; an empty value supplies metadata only.
type CatalogSession struct {
	SessionID     string
	SessionPath   string
	ResumeCommand []string
	CWD           string
	ProjectRoot   string
	ProcessPID    int
	Current       bool
	Activity      registry.Activity
	Attributes    map[string]string
	Title         string
}

// CatalogProvider is an optional adapter capability for native session lists.
// Providers may return partial results with an error.
type CatalogProvider interface {
	Catalog(ctx context.Context) ([]CatalogSession, error)
}

type ScreenManifestProvider interface {
	ScreenManifest() string
}

// Installable adapters describe the files an integration manages. InstallPlan
// returns [ErrHomeUnknown] when the target lies under a home directory that is
// not known.
type Installable interface {
	InstallPlan(binary string) (InstallPlan, error)
}

// NoInstallRequired marks adapters whose native integration is built into the
// harness and therefore needs no managed artifact or setup command.
type NoInstallRequired interface {
	NoInstallRequired()
}

type InstallAdvisor interface {
	InstallNextStep(changed bool, dryRun bool) string
	StatusNextStep(current bool, stale bool) string
}

type Resumable interface {
	ResumeCommand(sessionID string, sessionPath string) []string
}

type PayloadAdapter interface {
	PayloadCompatible(rawPayload json.RawMessage) bool
	PayloadDefaults(payload map[string]any) (PayloadDefaults, error)
}

// PayloadActivityAdapter refines the activity a generated hook declares when
// the native payload distinguishes states that the hook event alone cannot.
type PayloadActivityAdapter interface {
	PayloadActivity(
		event string,
		activity registry.Activity,
		payload map[string]any,
		at time.Time,
	) registry.Activity
}

type ActivityDetailer interface {
	ActivityDetail(
		event string,
		activity registry.Activity,
		attributes map[string]string,
	) *registry.ActivityDetail
}

type DetailCapabilityProvider interface {
	DetailCapabilities() registry.DetailCapabilities
}

type ProcessFilter interface {
	ObservableProcess(process processinfo.Process) bool
}

type DistributionProvider interface {
	Distribution() Distribution
}

type TranscriptProvider interface {
	Transcript() transcript.Reader
}

type BaseAdapter struct {
	definition Definition
}

func NewBaseAdapter(definition Definition) BaseAdapter {
	return BaseAdapter{definition: cloneDefinition(definition)}
}

func (adapter BaseAdapter) Definition() Definition {
	return cloneDefinition(adapter.definition)
}

func cloneDefinition(definition Definition) Definition {
	return Definition{
		ID:                        definition.ID,
		ExclusiveProcess:          definition.ExclusiveProcess,
		CatalogCreates:            definition.CatalogCreates,
		RetainNativeActivity:      definition.RetainNativeActivity,
		Aliases:                   slices.Clone(definition.Aliases),
		ProcessNames:              slices.Clone(definition.ProcessNames),
		Env:                       cloneEnvKeys(definition.Env),
		Capabilities:              definition.Capabilities,
		IntegrationVersion:        definition.IntegrationVersion,
		IntegrationSource:         definition.IntegrationSource,
		StateAuthority:            definition.StateAuthority,
		ScreenFallback:            definition.ScreenFallback,
		PayloadOmissions:          slices.Clone(definition.PayloadOmissions),
		LifecycleEventAttribute:   definition.LifecycleEventAttribute,
		LifecycleSourceAttributes: slices.Clone(definition.LifecycleSourceAttributes),
	}
}

func cloneEnvKeys(keys EnvKeys) EnvKeys {
	return EnvKeys{
		Config:      slices.Clone(keys.Config),
		SessionID:   slices.Clone(keys.SessionID),
		SessionPath: slices.Clone(keys.SessionPath),
		ProjectRoot: slices.Clone(keys.ProjectRoot),
		PID:         slices.Clone(keys.PID),
		Event:       slices.Clone(keys.Event),
	}
}
