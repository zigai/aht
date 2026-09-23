package registry

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	HarnessClaude   Harness = "claude"
	HarnessCodex    Harness = "codex"
	HarnessCursor   Harness = "cursor"
	HarnessCopilot  Harness = "copilot"
	HarnessCline    Harness = "cline"
	HarnessKimiCode Harness = "kimi-code"
	HarnessGrok     Harness = "grok"
	HarnessGoose    Harness = "goose"
	HarnessPi       Harness = "pi"
	HarnessOmp      Harness = "omp"
	HarnessOpenCode Harness = "opencode"
	HarnessAgy      Harness = "agy"
	HarnessKilo     Harness = "kilo"
	HarnessDroid    Harness = "droid"
	HarnessOpenClaw Harness = "openclaw"
	HarnessHermes   Harness = "hermes"
	HarnessAmp      Harness = "amp"
)

const (
	PresenceLive    Presence = "live"
	PresenceGone    Presence = "gone"
	PresenceUnknown Presence = "unknown"
)

const (
	ActivityRunning     Activity = "running"
	ActivityWaiting     Activity = "waiting"
	ActivityIdle        Activity = "idle"
	ActivityFailed      Activity = "failed"
	ActivityInterrupted Activity = "interrupted"
	ActivityUnknown     Activity = "unknown"
)

const (
	ObservationSourceNative      ObservationSource = "native"
	ObservationSourceProcess     ObservationSource = "process"
	ObservationSourceTmux        ObservationSource = "tmux"
	ObservationSourceMultiplexer ObservationSource = "multiplexer"
	ObservationSourceCatalog     ObservationSource = "catalog"
	ObservationSourceScreen      ObservationSource = "screen"
)

const (
	ObservationEvidenceNativeEvent         ObservationEvidence = "native_event"
	ObservationEvidenceProcessPresence     ObservationEvidence = "process_presence"
	ObservationEvidenceTmuxLocation        ObservationEvidence = "tmux_location"
	ObservationEvidenceMultiplexerLocation ObservationEvidence = "multiplexer_location"
	ObservationEvidenceCatalogMetadata     ObservationEvidence = "catalog_metadata"
	ObservationEvidenceScreenState         ObservationEvidence = "screen_state"
)

const (
	NativeLifecycleStart  NativeLifecycle = "start"
	NativeLifecycleResume NativeLifecycle = "resume"
	NativeLifecycleEnd    NativeLifecycle = "end"
)

const (
	MultiplexerTmux   MultiplexerKind = "tmux"
	MultiplexerZellij MultiplexerKind = "zellij"
	MultiplexerHerdr  MultiplexerKind = "herdr"
)

const (
	SummaryGroupByMultiplexerSession SummaryGroupBy = "multiplexer-session"
	SummaryGroupByProject            SummaryGroupBy = "project"
	SummaryGroupByHarness            SummaryGroupBy = "harness"
)

var (
	ErrUnknownHarness     = errors.New("unknown harness")
	ErrUnknownPresence    = errors.New("unknown presence")
	ErrUnknownActivity    = errors.New("unknown activity")
	ErrUnknownSource      = errors.New("unknown observation source")
	ErrUnknownEvidence    = errors.New("unknown observation evidence")
	ErrInvalidObservation = errors.New("invalid observation")
	ErrUnsupportedGroupBy = errors.New("unsupported summary group-by")

	allHarnesses = []Harness{
		HarnessClaude, HarnessCodex, HarnessCursor, HarnessCopilot, HarnessCline,
		HarnessKimiCode, HarnessGrok, HarnessGoose, HarnessPi, HarnessOmp,
		HarnessOpenCode, HarnessAgy, HarnessKilo, HarnessDroid, HarnessOpenClaw,
		HarnessHermes, HarnessAmp,
	}
)

type Harness string

type Presence string

type Activity string

type ObservationSource string

type ObservationEvidence string

type NativeLifecycle string

type TmuxContext struct {
	Inside          bool   `json:"inside"`
	ServerSocket    string `json:"server_socket,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	SessionName     string `json:"session_name,omitempty"`
	WindowID        string `json:"window_id,omitempty"`
	WindowIndex     string `json:"window_index,omitempty"`
	WindowName      string `json:"window_name,omitempty"`
	PaneID          string `json:"pane_id,omitempty"`
	PaneIndex       string `json:"pane_index,omitempty"`
	PaneCurrentPath string `json:"pane_current_path,omitempty"`
	PanePID         int    `json:"pane_pid,omitempty"`
	PaneTTY         string `json:"pane_tty,omitempty"`
	ClientTTY       string `json:"client_tty,omitempty"`
}

type MultiplexerKind string

// MultiplexerContext identifies an addressable terminal pane. Window fields
// represent tmux windows, while workspace and tab fields represent native
// Zellij and Herdr containers.
type MultiplexerContext struct {
	Kind            MultiplexerKind `json:"kind"`
	ServerID        string          `json:"server_id,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	SessionName     string          `json:"session_name,omitempty"`
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	WorkspaceName   string          `json:"workspace_name,omitempty"`
	TabID           string          `json:"tab_id,omitempty"`
	TabIndex        string          `json:"tab_index,omitempty"`
	TabName         string          `json:"tab_name,omitempty"`
	WindowID        string          `json:"window_id,omitempty"`
	WindowIndex     string          `json:"window_index,omitempty"`
	WindowName      string          `json:"window_name,omitempty"`
	PaneID          string          `json:"pane_id,omitempty"`
	PaneIndex       string          `json:"pane_index,omitempty"`
	PaneCurrentPath string          `json:"pane_current_path,omitempty"`
	PanePID         int             `json:"pane_pid,omitempty"`
	PaneTTY         string          `json:"pane_tty,omitempty"`
	ClientTTY       string          `json:"client_tty,omitempty"`
}

type ProcessIdentity struct {
	PID            int    `json:"pid"`
	PPID           int    `json:"ppid"`
	ProcessGroupID int    `json:"process_group_id"`
	Foreground     bool   `json:"foreground"`
	StartIdentity  string `json:"start_identity"`
	Executable     string `json:"executable"`
	CWD            string `json:"cwd"`
	TTY            string `json:"tty"`
}

type NativeObservation struct {
	Event                 string            `json:"event,omitempty"`
	Lifecycle             *NativeLifecycle  `json:"lifecycle,omitempty"`
	Presence              *Presence         `json:"presence,omitempty"`
	Activity              *Activity         `json:"activity,omitempty"`
	ActivityAuthoritative *bool             `json:"activity_authoritative,omitempty"`
	Sequence              *uint64           `json:"sequence,omitempty"`
	SessionID             string            `json:"session_id,omitempty"`
	SessionPath           string            `json:"session_path,omitempty"`
	ObservedAt            time.Time         `json:"observed_at"`
	Attributes            map[string]string `json:"attributes,omitempty"`
	RawPayload            json.RawMessage   `json:"raw_payload,omitempty"`
	Process               ProcessIdentity   `json:"process,omitzero"`
}

type ScreenObservation struct {
	Activity               Activity        `json:"activity"`
	Authority              string          `json:"authority"`
	Reason                 string          `json:"reason"`
	RuleID                 string          `json:"rule_id,omitempty"`
	ManifestSource         string          `json:"manifest_source,omitempty"`
	ManifestVersion        int             `json:"manifest_version,omitempty"`
	FallbackForIntegration string          `json:"fallback_for_integration,omitempty"`
	FallbackReason         string          `json:"fallback_reason,omitempty"`
	Process                ProcessIdentity `json:"process"`
	ObservedAt             time.Time       `json:"observed_at"`
}

type ActivityDecision struct {
	Authority       string          `json:"authority"`
	Reason          string          `json:"reason"`
	RuleID          string          `json:"rule_id,omitempty"`
	ManifestSource  string          `json:"manifest_source,omitempty"`
	ManifestVersion int             `json:"manifest_version,omitempty"`
	FallbackReason  string          `json:"fallback_reason,omitempty"`
	Process         ProcessIdentity `json:"process,omitzero"`
	ObservedAt      time.Time       `json:"observed_at"`
}

type ProcessObservation struct {
	Present    bool            `json:"present"`
	Process    ProcessIdentity `json:"process"`
	ObservedAt time.Time       `json:"observed_at"`
}

type TmuxObservation struct {
	Process    ProcessIdentity `json:"process"`
	Context    TmuxContext     `json:"context"`
	ObservedAt time.Time       `json:"observed_at"`
}

type MultiplexerObservation struct {
	Process    ProcessIdentity    `json:"process"`
	Context    MultiplexerContext `json:"context"`
	ObservedAt time.Time          `json:"observed_at"`
}

type CatalogObservation struct {
	SessionID     string    `json:"session_id,omitempty"`
	SessionPath   string    `json:"session_path,omitempty"`
	ResumeCommand []string  `json:"resume_command,omitempty"`
	CWD           string    `json:"cwd,omitempty"`
	ProjectRoot   string    `json:"project_root,omitempty"`
	ProcessPID    int       `json:"process_pid,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}

type Observations struct {
	Native      *NativeObservation      `json:"native,omitempty"`
	Process     *ProcessObservation     `json:"process,omitempty"`
	Tmux        *TmuxObservation        `json:"tmux,omitempty"`
	Multiplexer *MultiplexerObservation `json:"multiplexer,omitempty"`
	Catalog     *CatalogObservation     `json:"catalog,omitempty"`
	Screen      *ScreenObservation      `json:"screen,omitempty"`
}

type Session struct {
	SchemaVersion     int                `json:"schema_version"`
	ID                string             `json:"id"`
	Harness           Harness            `json:"harness"`
	Presence          Presence           `json:"presence"`
	Activity          *Activity          `json:"activity"`
	SessionID         string             `json:"session_id,omitempty"`
	SessionPath       string             `json:"session_path,omitempty"`
	ResumeCommand     []string           `json:"resume_command,omitempty"`
	CWD               string             `json:"cwd,omitempty"`
	ProjectRoot       string             `json:"project_root,omitempty"`
	Process           *ProcessIdentity   `json:"process,omitempty"`
	Tmux              TmuxContext        `json:"tmux,omitzero"`
	Multiplexer       MultiplexerContext `json:"multiplexer,omitzero"`
	Observations      Observations       `json:"observations"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	PresenceChangedAt time.Time          `json:"presence_changed_at"`
	ActivityChangedAt time.Time          `json:"activity_changed_at"`
	ActivityDecision  *ActivityDecision  `json:"activity_decision,omitempty"`
}

type ObservationIdentity struct {
	SessionID   string `json:"session_id,omitempty"`
	SessionPath string `json:"session_path,omitempty"`
}

type CatalogMetadata struct {
	ResumeCommand []string `json:"resume_command,omitempty"`
	CWD           string   `json:"cwd,omitempty"`
	ProjectRoot   string   `json:"project_root,omitempty"`
	ProcessPID    int      `json:"process_pid,omitempty"`
	Current       bool     `json:"-"`
}

type Observation struct {
	Source                ObservationSource   `json:"source"`
	Evidence              ObservationEvidence `json:"evidence"`
	Harness               Harness             `json:"harness"`
	Identity              ObservationIdentity `json:"identity"`
	Lifecycle             *NativeLifecycle    `json:"lifecycle,omitempty"`
	Presence              *Presence           `json:"presence,omitempty"`
	Activity              *Activity           `json:"activity,omitempty"`
	ActivityAuthoritative *bool               `json:"activity_authoritative,omitempty"`
	Sequence              *uint64             `json:"sequence,omitempty"`
	NativeEvent           string              `json:"native_event,omitempty"`
	ProcessPresent        *bool               `json:"process_present,omitempty"`
	Process               *ProcessIdentity    `json:"process,omitempty"`
	Tmux                  *TmuxContext        `json:"tmux,omitempty"`
	Multiplexer           *MultiplexerContext `json:"multiplexer,omitempty"`
	Catalog               *CatalogMetadata    `json:"catalog,omitempty"`
	Attributes            map[string]string   `json:"attributes,omitempty"`
	RawPayload            json.RawMessage     `json:"raw_payload,omitempty"`
	Screen                *ScreenObservation  `json:"screen,omitempty"`
	ObservedAt            time.Time           `json:"observed_at"`
}

// Filter retains its original JSON field names for broker protocol compatibility.
// New path filters are resolved on the calling machine before an RPC.
type Filter struct {
	Harness            Harness         `json:"Harness"`            //nolint:tagliatelle // Preserve existing broker protocol field names.
	Presence           Presence        `json:"Presence"`           //nolint:tagliatelle // Preserve existing broker protocol field names.
	Activity           Activity        `json:"Activity"`           //nolint:tagliatelle // Preserve existing broker protocol field names.
	TmuxSession        string          `json:"TmuxSession"`        //nolint:tagliatelle // Preserve existing broker protocol field names.
	MultiplexerSession string          `json:"MultiplexerSession"` //nolint:tagliatelle // Preserve existing broker protocol field names.
	Project            string          `json:"project,omitempty"`
	ProjectSubtree     bool            `json:"project_subtree,omitempty"`
	CWD                string          `json:"cwd,omitempty"`
	MultiplexerKind    MultiplexerKind `json:"multiplexer_kind,omitempty"`
	MultiplexerServer  string          `json:"multiplexer_server,omitempty"`
	MultiplexerPane    string          `json:"multiplexer_pane,omitempty"`
}

// SummaryGroupBy identifies the grouping dimension for aggregate session summaries.
type SummaryGroupBy string

// SummaryOptions configures grouping for aggregate session summaries.
type SummaryOptions struct {
	GroupBy SummaryGroupBy `json:"group_by,omitempty"`
}

type Summary struct {
	GroupBy                SummaryGroupBy  `json:"group_by,omitempty"`
	GroupKey               string          `json:"group_key,omitempty"`
	GroupLabel             string          `json:"group_label,omitempty"`
	Project                string          `json:"project,omitempty"`
	ProjectRoot            string          `json:"project_root,omitempty"`
	Harness                Harness         `json:"harness,omitempty"`
	MultiplexerKind        MultiplexerKind `json:"multiplexer_kind,omitempty"`
	MultiplexerServerID    string          `json:"multiplexer_server_id,omitempty"`
	MultiplexerSessionID   string          `json:"multiplexer_session_id,omitempty"`
	MultiplexerSessionName string          `json:"multiplexer_session_name,omitempty"`
	TmuxSessionID          string          `json:"tmux_session_id,omitempty"`
	TmuxSessionName        string          `json:"tmux_session_name,omitempty"`
	Total                  int             `json:"total"`
	Live                   int             `json:"live"`
	Gone                   int             `json:"gone"`
	PresenceUnknown        int             `json:"presence_unknown"`
	Running                int             `json:"running"`
	Waiting                int             `json:"waiting"`
	Idle                   int             `json:"idle"`
	Failed                 int             `json:"failed"`
	Interrupted            int             `json:"interrupted"`
	ActivityUnknown        int             `json:"activity_unknown"`
}

func (h Harness) IsValid() bool {
	switch h {
	case HarnessClaude, HarnessCodex, HarnessCursor, HarnessCopilot, HarnessCline,
		HarnessKimiCode, HarnessGrok, HarnessGoose, HarnessPi, HarnessOmp,
		HarnessOpenCode, HarnessAgy, HarnessKilo, HarnessDroid, HarnessOpenClaw,
		HarnessHermes, HarnessAmp:
		return true
	}
	return false
}

// ExclusiveProcessSessions reports whether a process hosts at most one native session.
func (h Harness) ExclusiveProcessSessions() bool {
	switch h {
	case HarnessOpenClaw:
		return false
	case HarnessClaude, HarnessCodex, HarnessCursor, HarnessCopilot, HarnessCline,
		HarnessKimiCode, HarnessGrok, HarnessGoose, HarnessPi, HarnessOmp,
		HarnessOpenCode, HarnessAgy, HarnessKilo, HarnessDroid, HarnessHermes,
		HarnessAmp:
		return true
	default:
		return true
	}
}

func (g SummaryGroupBy) IsValid() bool {
	switch g {
	case SummaryGroupByMultiplexerSession, SummaryGroupByProject, SummaryGroupByHarness:
		return true
	default:
		return false
	}
}

// AllHarnesses returns a slice containing every canonical harness.
func AllHarnesses() []Harness {
	return slices.Clone(allHarnesses)
}

func (p Presence) IsValid() bool {
	switch p {
	case PresenceLive, PresenceGone, PresenceUnknown:
		return true
	}
	return false
}

func (a Activity) IsValid() bool {
	switch a {
	case ActivityRunning, ActivityWaiting, ActivityIdle, ActivityFailed, ActivityInterrupted, ActivityUnknown:
		return true
	}
	return false
}

func (s ObservationSource) IsValid() bool {
	switch s {
	case ObservationSourceNative, ObservationSourceProcess, ObservationSourceTmux,
		ObservationSourceMultiplexer, ObservationSourceCatalog, ObservationSourceScreen:
		return true
	}
	return false
}

func (e ObservationEvidence) IsValid() bool {
	switch e {
	case ObservationEvidenceNativeEvent, ObservationEvidenceProcessPresence,
		ObservationEvidenceTmuxLocation, ObservationEvidenceMultiplexerLocation,
		ObservationEvidenceCatalogMetadata, ObservationEvidenceScreenState:
		return true
	}
	return false
}

func (l NativeLifecycle) IsValid() bool {
	switch l {
	case NativeLifecycleStart, NativeLifecycleResume, NativeLifecycleEnd:
		return true
	}
	return false
}

func (k MultiplexerKind) IsValid() bool {
	switch k {
	case MultiplexerTmux, MultiplexerZellij, MultiplexerHerdr:
		return true
	}
	return false
}

func (c TmuxContext) Empty() bool { return c == (TmuxContext{}) } //nolint:exhaustruct_v5 // zero-value comparison

func (c MultiplexerContext) Empty() bool { return c == (MultiplexerContext{}) } //nolint:exhaustruct_v5 // zero-value comparison

func MultiplexerFromTmux(c TmuxContext) MultiplexerContext {
	if c.Empty() {
		var empty MultiplexerContext
		return empty
	}
	return MultiplexerContext{
		Kind: MultiplexerTmux, ServerID: c.ServerSocket, SessionID: c.SessionID, SessionName: c.SessionName,
		WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "",
		WindowID: c.WindowID, WindowIndex: c.WindowIndex, WindowName: c.WindowName,
		PaneID: c.PaneID, PaneIndex: c.PaneIndex, PaneCurrentPath: c.PaneCurrentPath,
		PanePID: c.PanePID, PaneTTY: c.PaneTTY, ClientTTY: c.ClientTTY,
	}
}

func (c MultiplexerContext) TmuxContext() TmuxContext {
	if c.Kind != MultiplexerTmux {
		var empty TmuxContext
		return empty
	}
	return TmuxContext{
		Inside: true, ServerSocket: c.ServerID, SessionID: c.SessionID, SessionName: c.SessionName,
		WindowID: c.WindowID, WindowIndex: c.WindowIndex, WindowName: c.WindowName,
		PaneID: c.PaneID, PaneIndex: c.PaneIndex, PaneCurrentPath: c.PaneCurrentPath,
		PanePID: c.PanePID, PaneTTY: c.PaneTTY, ClientTTY: c.ClientTTY,
	}
}

func (p ProcessIdentity) Complete() bool { return p.PID > 0 && p.StartIdentity != "" }

func (p ProcessIdentity) Equal(other ProcessIdentity) bool {
	return p.PID == other.PID && p.StartIdentity != "" && p.StartIdentity == other.StartIdentity
}

func NormalizePresence(value string) (Presence, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case string(PresenceLive):
		return PresenceLive, nil
	case string(PresenceGone):
		return PresenceGone, nil
	case string(PresenceUnknown):
		return PresenceUnknown, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownPresence, value)
	}
}

func NormalizeActivity(value string) (Activity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case string(ActivityRunning), "working", "busy", "retry":
		return ActivityRunning, nil
	case string(ActivityWaiting), "blocked":
		return ActivityWaiting, nil
	case string(ActivityIdle), "offline":
		return ActivityIdle, nil
	case string(ActivityFailed), "error", "errored", "crash", "crashed":
		return ActivityFailed, nil
	case string(ActivityInterrupted), "paused", "stopped", "canceled", "cancelled": //nolint:misspell // British spelling remains a supported input alias.
		return ActivityInterrupted, nil
	case string(ActivityUnknown):
		return ActivityUnknown, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownActivity, value)
	}
}

func NormalizeSource(value string) (ObservationSource, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ObservationSourceNative):
		return ObservationSourceNative, nil
	case string(ObservationSourceProcess):
		return ObservationSourceProcess, nil
	case string(ObservationSourceTmux):
		return ObservationSourceTmux, nil
	case string(ObservationSourceMultiplexer):
		return ObservationSourceMultiplexer, nil
	case string(ObservationSourceCatalog):
		return ObservationSourceCatalog, nil
	case string(ObservationSourceScreen):
		return ObservationSourceScreen, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownSource, value)
	}
}

func NormalizeEvidence(value string) (ObservationEvidence, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ObservationEvidenceNativeEvent):
		return ObservationEvidenceNativeEvent, nil
	case string(ObservationEvidenceProcessPresence):
		return ObservationEvidenceProcessPresence, nil
	case string(ObservationEvidenceTmuxLocation):
		return ObservationEvidenceTmuxLocation, nil
	case string(ObservationEvidenceMultiplexerLocation):
		return ObservationEvidenceMultiplexerLocation, nil
	case string(ObservationEvidenceCatalogMetadata):
		return ObservationEvidenceCatalogMetadata, nil
	case string(ObservationEvidenceScreenState):
		return ObservationEvidenceScreenState, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownEvidence, value)
	}
}

func NormalizeLifecycle(value string) (NativeLifecycle, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(NativeLifecycleStart):
		return NativeLifecycleStart, nil
	case string(NativeLifecycleResume):
		return NativeLifecycleResume, nil
	case string(NativeLifecycleEnd):
		return NativeLifecycleEnd, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidObservation, value)
	}
}

// Validate enforces source-specific evidence invariants for an observation.
//
//nolint:gocognit,cyclop,maintidx // validation enforces source-specific evidence invariants in one place
func (observation Observation) Validate() error {
	if observation.Harness == "" {
		return fmt.Errorf("%w: %w", ErrInvalidObservation, ErrHarnessRequired)
	}
	if !observation.Harness.IsValid() {
		return fmt.Errorf("%w: harness %q is not canonical", ErrInvalidObservation, observation.Harness)
	}
	if observation.Lifecycle != nil && !observation.Lifecycle.IsValid() {
		return fmt.Errorf("%w: lifecycle %q is invalid", ErrInvalidObservation, *observation.Lifecycle)
	}
	if observation.Presence != nil && !observation.Presence.IsValid() {
		return fmt.Errorf("%w: presence %q is invalid", ErrInvalidObservation, *observation.Presence)
	}
	if observation.Activity != nil && !observation.Activity.IsValid() {
		return fmt.Errorf("%w: activity %q is invalid", ErrInvalidObservation, *observation.Activity)
	}
	if observation.Process != nil && (!observation.Process.Complete() || observation.Process.PPID < 0 || observation.Process.ProcessGroupID < 0) {
		return fmt.Errorf("%w: process identity is invalid", ErrInvalidObservation)
	}
	if observation.Tmux != nil && observation.Tmux.PanePID < 0 {
		return fmt.Errorf("%w: tmux pane pid is invalid", ErrInvalidObservation)
	}
	if observation.Multiplexer != nil {
		if observation.Multiplexer.PanePID < 0 {
			return fmt.Errorf("%w: multiplexer pane pid is invalid", ErrInvalidObservation)
		}
		if !observation.Multiplexer.Empty() && !observation.Multiplexer.Kind.IsValid() {
			return fmt.Errorf("%w: multiplexer kind %q is invalid", ErrInvalidObservation, observation.Multiplexer.Kind)
		}
	}
	if observation.Catalog != nil && observation.Catalog.ProcessPID < 0 {
		return fmt.Errorf("%w: catalog process pid is invalid", ErrInvalidObservation)
	}
	if observation.Source == "" || observation.Evidence == "" {
		return fmt.Errorf("%w: source and evidence are required", ErrInvalidObservation)
	}
	if observation.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at is required", ErrInvalidObservation)
	}
	if observation.Source != ObservationSourceNative && observation.Source != ObservationSourceScreen && observation.Activity != nil {
		return fmt.Errorf("%w: activity is not accepted for source %q", ErrInvalidObservation, observation.Source)
	}
	if observation.Source != ObservationSourceNative && observation.ActivityAuthoritative != nil {
		return fmt.Errorf("%w: activity authority is only accepted for native observations", ErrInvalidObservation)
	}
	if observation.Sequence != nil {
		if observation.Source != ObservationSourceNative {
			return fmt.Errorf("%w: sequence is only accepted for native observations", ErrInvalidObservation)
		}
		if strings.TrimSpace(observation.Attributes["aht_integration"]) == "" {
			return fmt.Errorf("%w: sequenced observation requires aht_integration", ErrInvalidObservation)
		}
	}
	pairOK := (observation.Source == ObservationSourceNative && observation.Evidence == ObservationEvidenceNativeEvent) ||
		(observation.Source == ObservationSourceProcess && observation.Evidence == ObservationEvidenceProcessPresence) ||
		(observation.Source == ObservationSourceTmux && observation.Evidence == ObservationEvidenceTmuxLocation) ||
		(observation.Source == ObservationSourceMultiplexer && observation.Evidence == ObservationEvidenceMultiplexerLocation) ||
		(observation.Source == ObservationSourceCatalog && observation.Evidence == ObservationEvidenceCatalogMetadata) ||
		(observation.Source == ObservationSourceScreen && observation.Evidence == ObservationEvidenceScreenState)
	if !pairOK {
		return fmt.Errorf("%w: source %q does not accept evidence %q", ErrInvalidObservation, observation.Source, observation.Evidence)
	}
	if observation.Source == ObservationSourceNative {
		if observation.Lifecycle != nil && *observation.Lifecycle == NativeLifecycleEnd && observation.Activity != nil {
			return fmt.Errorf("%w: end cannot include activity", ErrInvalidObservation)
		}
		if observation.Lifecycle == nil && observation.Presence == nil && observation.Activity == nil && observation.NativeEvent == "" {
			return fmt.Errorf("%w: native event or transition is required", ErrInvalidObservation)
		}
	}
	if observation.Source == ObservationSourceProcess {
		if observation.ProcessPresent == nil {
			return fmt.Errorf("%w: process_present is required", ErrInvalidObservation)
		}
		if *observation.ProcessPresent {
			if observation.Process == nil {
				return fmt.Errorf("%w: complete process identity is required", ErrInvalidObservation)
			}
		}
	}
	if observation.Source == ObservationSourceTmux {
		if observation.Tmux == nil {
			return fmt.Errorf("%w: tmux context is required", ErrInvalidObservation)
		}
		if observation.Process == nil {
			return fmt.Errorf("%w: complete process identity is required", ErrInvalidObservation)
		}
	}
	if observation.Source == ObservationSourceMultiplexer {
		if observation.Multiplexer == nil {
			return fmt.Errorf("%w: multiplexer context is required", ErrInvalidObservation)
		}
		if observation.Process == nil {
			return fmt.Errorf("%w: complete process identity is required", ErrInvalidObservation)
		}
	}
	if observation.Source == ObservationSourceCatalog && observation.Catalog == nil {
		return fmt.Errorf("%w: catalog metadata is required", ErrInvalidObservation)
	}
	if observation.Source == ObservationSourceScreen {
		if observation.Activity == nil || observation.Screen == nil {
			return fmt.Errorf("%w: screen activity and evidence are required", ErrInvalidObservation)
		}
		if observation.Process == nil {
			return fmt.Errorf("%w: complete process identity is required", ErrInvalidObservation)
		}
		if !observation.Screen.Process.Equal(*observation.Process) {
			return fmt.Errorf("%w: screen process does not match observation process", ErrInvalidObservation)
		}
	}
	if observation.Identity.SessionID == "" && observation.Identity.SessionPath == "" && observation.Process == nil {
		return fmt.Errorf("%w: identity is required", ErrInvalidObservation)
	}
	return nil
}

// SessionCWD returns the best available working directory for session.
func SessionCWD(session Session) string {
	if session.CWD != "" {
		return session.CWD
	}
	if session.Process != nil && session.Process.CWD != "" {
		return session.Process.CWD
	}
	return session.Multiplexer.PaneCurrentPath
}

// CanonicalPath returns an absolute, symlink-resolved, cleaned path for p.
func CanonicalPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	return canonicalizeExistingOrParent(cleaned)
}

// PathsEqual reports whether p1 and p2 resolve to the same canonical path.
func PathsEqual(p1, p2 string) bool {
	if p1 == "" || p2 == "" {
		return false
	}
	if p1 == p2 {
		return true
	}
	return CanonicalPath(p1) == CanonicalPath(p2)
}

// PathWithinOrEqual reports whether target is equal to base or located within base.
func PathWithinOrEqual(target, base string) bool {
	targetCanon := CanonicalPath(target)
	baseCanon := CanonicalPath(base)
	if targetCanon == "" || baseCanon == "" {
		return false
	}
	if targetCanon == baseCanon {
		return true
	}
	rel, err := filepath.Rel(baseCanon, targetCanon)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// MatchesProject reports whether session matches the specified project path.
func MatchesProject(session Session, project string, subtree bool) bool {
	if project == "" {
		return true
	}
	sessionProj := session.ProjectRoot
	if sessionProj == "" {
		sessionProj = SessionCWD(session)
	}
	if subtree {
		return PathWithinOrEqual(session.ProjectRoot, project) ||
			PathWithinOrEqual(SessionCWD(session), project)
	}
	return PathsEqual(sessionProj, project)
}

// MatchesServer reports whether session matches server on either Multiplexer or Tmux context.
func MatchesServer(session Session, server string) bool {
	if session.Multiplexer.ServerID == server || session.Tmux.ServerSocket == server {
		return true
	}
	return PathsEqual(session.Multiplexer.ServerID, server) || PathsEqual(session.Tmux.ServerSocket, server)
}

// NormalizePaths resolves project and CWD selectors before they cross a process boundary.
func (filter Filter) NormalizePaths() Filter {
	filter.Project = CanonicalPath(filter.Project)
	filter.CWD = CanonicalPath(filter.CWD)
	return filter
}

// FilterSessions returns sessions matching filter in deterministic sort order.
//
//nolint:cyclop,gocognit // each filter dimension is intentionally independent
func FilterSessions(sessions []Session, filter Filter) []Session {
	filtered := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		if filter.Harness != "" && session.Harness != filter.Harness {
			continue
		}
		if filter.Presence != "" && session.Presence != filter.Presence {
			continue
		}
		if filter.Activity != "" && (session.Activity == nil || *session.Activity != filter.Activity) {
			continue
		}
		if filter.TmuxSession != "" && session.Tmux.SessionName != filter.TmuxSession && session.Tmux.SessionID != filter.TmuxSession {
			continue
		}
		if filter.MultiplexerSession != "" && session.Multiplexer.SessionName != filter.MultiplexerSession && session.Multiplexer.SessionID != filter.MultiplexerSession {
			continue
		}
		if filter.CWD != "" && !PathsEqual(SessionCWD(session), filter.CWD) {
			continue
		}
		if filter.Project != "" && !MatchesProject(session, filter.Project, filter.ProjectSubtree) {
			continue
		}
		if !matchesFilterLocation(session, filter) {
			continue
		}
		filtered = append(filtered, session)
	}
	sortSessions(filtered)
	return filtered
}

func sessionIDForObservation(observation Observation) string {
	parts := []string{string(observation.Harness)}
	switch {
	case observation.Identity.SessionID != "":
		parts = append(parts, "id", observation.Identity.SessionID)
	case observation.Identity.SessionPath != "":
		parts = append(parts, "path", filepath.Clean(observation.Identity.SessionPath))
	case observation.Process != nil && observation.Process.Complete():
		parts = append(parts, "process", strconv.Itoa(observation.Process.PID), observation.Process.StartIdentity)
	default:
		parts = append(parts, "event", observation.NativeEvent)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return string(observation.Harness) + "-" + hex.EncodeToString(sum[:8])
}

func matchesFilterLocation(session Session, filter Filter) bool {
	populateMultiplexerProjection(&session)
	if filter.MultiplexerKind != "" && session.Multiplexer.Kind != filter.MultiplexerKind {
		return false
	}
	if filter.MultiplexerServer != "" && !MatchesServer(session, filter.MultiplexerServer) {
		return false
	}
	if filter.MultiplexerPane != "" &&
		session.Multiplexer.PaneID != filter.MultiplexerPane &&
		session.Tmux.PaneID != filter.MultiplexerPane {
		return false
	}
	return true
}

func sortSessions(sessions []Session) {
	slices.SortFunc(sessions, func(left, right Session) int {
		return cmp.Or(
			cmp.Compare(left.Multiplexer.Kind, right.Multiplexer.Kind),
			cmp.Compare(left.Multiplexer.SessionName, right.Multiplexer.SessionName),
			compareNumericStrings(
				cmp.Or(left.Multiplexer.WindowIndex, left.Multiplexer.TabIndex),
				cmp.Or(right.Multiplexer.WindowIndex, right.Multiplexer.TabIndex),
			),
			compareNumericStrings(left.Multiplexer.PaneIndex, right.Multiplexer.PaneIndex),
			cmp.Compare(left.Harness, right.Harness),
			cmp.Compare(left.ID, right.ID),
			left.UpdatedAt.Compare(right.UpdatedAt),
		)
	})
}

func compareNumericStrings(left, right string) int {
	leftNumber, leftErr := strconv.Atoi(left)
	rightNumber, rightErr := strconv.Atoi(right)
	if leftErr == nil && rightErr == nil {
		return cmp.Compare(leftNumber, rightNumber)
	}
	return strings.Compare(left, right)
}

func maxTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func canonicalizeExistingOrParent(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(path)
	if parent == path || parent == "." || parent == "/" || parent == string(filepath.Separator) {
		return path
	}
	resolvedParent := canonicalizeExistingOrParent(parent)
	return filepath.Join(resolvedParent, filepath.Base(path))
}
