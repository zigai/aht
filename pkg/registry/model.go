package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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

const (
	Provisional IdentityState = "provisional"
	Identified  IdentityState = "identified"
)

var (
	ErrUnknownHarness     = errors.New("unknown harness")
	ErrUnknownPresence    = errors.New("unknown presence")
	ErrUnknownActivity    = errors.New("unknown activity")
	ErrInvalidObservation = errors.New("invalid observation")
	ErrUnsupportedGroupBy = errors.New("unsupported summary group-by")
)

type IdentityState string

type Harness string

type Presence string

type Activity string

type NativeLifecycle string

type MultiplexerKind string

// Location identifies an addressable terminal pane. Window fields
// represent tmux windows, while workspace and tab fields represent native
// Zellij and Herdr containers.
type Location struct {
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

type Reporter struct {
	Sequence     *uint64 `json:"sequence,omitempty"`
	Integration  string  `json:"integration"`
	Version      int     `json:"version"`
	MultiSession bool    `json:"multi_session"`
}

type NativeObservation struct {
	Reporter    Reporter          `json:"reporter"`
	Event       string            `json:"event,omitempty"`
	Lifecycle   *NativeLifecycle  `json:"lifecycle,omitempty"`
	Presence    *Presence         `json:"presence,omitempty"`
	Activity    *Activity         `json:"activity,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
	SessionPath string            `json:"session_path,omitempty"`
	ObservedAt  time.Time         `json:"observed_at"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	RawPayload  json.RawMessage   `json:"raw_payload,omitempty"`
	Process     ProcessIdentity   `json:"process,omitzero"`
}

type ScreenObservation struct {
	Activity               Activity        `json:"activity"`
	Authority              Authority       `json:"authority"`
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
	Authority       Authority       `json:"authority"`
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

type MultiplexerObservation struct {
	Process    ProcessIdentity `json:"process"`
	Context    Location        `json:"context"`
	ObservedAt time.Time       `json:"observed_at"`
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
	Native   *NativeObservation      `json:"native,omitempty"`
	Process  *ProcessObservation     `json:"process,omitempty"`
	Location *MultiplexerObservation `json:"location,omitempty"`
	Catalog  *CatalogObservation     `json:"catalog,omitempty"`
	Screen   *ScreenObservation      `json:"screen,omitempty"`
}

//nolint:recvcheck // Value marshaling must also apply to sessions stored in maps.
type Session struct {
	Incarnation       Incarnation      `json:"incarnation"`
	IdentityState     IdentityState    `json:"identity_state"`
	Liveness          Liveness         `json:"-"`
	SchemaVersion     int              `json:"schema_version"`
	ID                string           `json:"id"`
	Harness           Harness          `json:"harness"`
	SessionID         string           `json:"session_id,omitempty"`
	SessionPath       string           `json:"session_path,omitempty"`
	ResumeCommand     []string         `json:"resume_command,omitempty"`
	CWD               string           `json:"cwd,omitempty"`
	ProjectRoot       string           `json:"project_root,omitempty"`
	Process           *ProcessIdentity `json:"process,omitempty"`
	Location          Location         `json:"location,omitzero"`
	Observations      Observations     `json:"observations"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	PresenceChangedAt time.Time        `json:"presence_changed_at"`
	ActivityChangedAt time.Time        `json:"activity_changed_at"`
}

type ObservationIdentity struct {
	SessionID   string            `json:"session_id,omitempty"`
	SessionPath string            `json:"session_path,omitempty"`
	CWD         string            `json:"cwd,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

type Listing struct {
	ResumeCommand []string `json:"resume_command,omitempty"`
	CWD           string   `json:"cwd,omitempty"`
	ProjectRoot   string   `json:"project_root,omitempty"`
	ProcessPID    int      `json:"process_pid,omitempty"`
	Current       bool     `json:"current"`
}

type Filter struct {
	Harness            Harness         `json:"harness"`
	Presence           Presence        `json:"presence"`
	Activity           Activity        `json:"activity"`
	MultiplexerSession string          `json:"multiplexer_session"`
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

func (g SummaryGroupBy) IsValid() bool {
	switch g {
	case SummaryGroupByMultiplexerSession, SummaryGroupByProject, SummaryGroupByHarness:
		return true
	default:
		return false
	}
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

func (c Location) Empty() bool {
	return c == (Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""})
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
