package harness

import (
	"encoding/json"
	"slices"

	"github.com/zigai/aht/pkg/registry"
)

const (
	IntegrationVersion = 8

	EnvSessionID   EnvField = "session_id"
	EnvSessionPath EnvField = "session_path"
	EnvProjectRoot EnvField = "project_root"
	EnvPID         EnvField = "pid"
	EnvEvent       EnvField = "event"
)

type EnvField string

type EnvKeys struct {
	SessionID   []string
	SessionPath []string
	ProjectRoot []string
	PID         []string
	Event       []string
}

type PayloadDefaults struct {
	SessionID   string
	SessionPath string
	CWD         string
	ProjectRoot string
	Event       string
	Attributes  map[string]string
}

type Capabilities struct {
	SessionStart      bool
	SessionEnd        bool
	RunningIdle       bool
	WaitingPermission bool
	ProcessIdentity   bool
	NativeCatalog     bool
	TTYTmuxContext    bool
}

type Definition struct {
	ID                 registry.Harness
	Aliases            []string
	ProcessNames       []string
	Env                EnvKeys
	Capabilities       Capabilities
	IntegrationVersion int
}

type Adapter interface {
	Definition() Definition
}

type ScreenManifestProvider interface {
	ScreenManifest() string
}

type Installable interface {
	InstallPlan(binary string) InstallPlan
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
		ID:                 definition.ID,
		Aliases:            slices.Clone(definition.Aliases),
		ProcessNames:       slices.Clone(definition.ProcessNames),
		Env:                cloneEnvKeys(definition.Env),
		Capabilities:       definition.Capabilities,
		IntegrationVersion: definition.IntegrationVersion,
	}
}

func cloneEnvKeys(keys EnvKeys) EnvKeys {
	return EnvKeys{
		SessionID:   slices.Clone(keys.SessionID),
		SessionPath: slices.Clone(keys.SessionPath),
		ProjectRoot: slices.Clone(keys.ProjectRoot),
		PID:         slices.Clone(keys.PID),
		Event:       slices.Clone(keys.Event),
	}
}
