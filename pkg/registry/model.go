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
	IdentityProvisional IdentityState = "provisional"
	IdentityIdentified  IdentityState = "identified"
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
	NativeEvidence

	Detail      *DetailEvidence `json:"detail,omitempty"`
	Presence    *Presence       `json:"presence,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	SessionPath string          `json:"session_path,omitempty"`
	ObservedAt  time.Time       `json:"observed_at"`
	RawPayload  json.RawMessage `json:"raw_payload,omitempty"`
	Process     ProcessIdentity `json:"process,omitzero"`
}

type ScreenObservation struct {
	Detail                 ActivityDetail  `json:"detail,omitempty"`
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
	Sighting   Sighting  `json:"-"`
	ObservedAt time.Time `json:"observed_at"`
}

type MultiplexerObservation struct {
	Placement  Placement `json:"-"`
	ObservedAt time.Time `json:"observed_at"`
}

type CatalogObservation struct {
	ListingMetadata

	SessionID   string    `json:"session_id,omitempty"`
	SessionPath string    `json:"session_path,omitempty"`
	ObservedAt  time.Time `json:"observed_at"`
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
	Detail            *StateDetail     `json:"detail,omitempty"`
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

type ListingMetadata struct {
	ResumeCommand []string `json:"resume_command,omitempty"`
	CWD           string   `json:"cwd,omitempty"`
	ProjectRoot   string   `json:"project_root,omitempty"`
	//exhaustruct:optional
	ProcessPID int `json:"process_pid,omitempty"`
}

type Listing struct {
	ListingMetadata

	//exhaustruct:optional
	ProcessStartIdentity string `json:"process_start_identity,omitempty"`
	//exhaustruct:optional
	Current bool `json:"current"`
}

//exhaustruct:optional
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

func (g SummaryGroupBy) Valid() bool {
	switch g {
	case SummaryGroupByMultiplexerSession, SummaryGroupByProject, SummaryGroupByHarness:
		return true
	default:
		return false
	}
}

func (p Presence) Valid() bool {
	switch p {
	case PresenceLive, PresenceGone, PresenceUnknown:
		return true
	}

	return false
}

func (a Activity) Valid() bool {
	switch a {
	case ActivityRunning,
		ActivityWaiting,
		ActivityIdle,
		ActivityFailed,
		ActivityInterrupted,
		ActivityUnknown:
		return true
	}

	return false
}

func (l NativeLifecycle) Valid() bool {
	switch l {
	case NativeLifecycleStart, NativeLifecycleResume, NativeLifecycleEnd:
		return true
	}

	return false
}

func (k MultiplexerKind) Valid() bool {
	switch k {
	case MultiplexerTmux, MultiplexerZellij, MultiplexerHerdr:
		return true
	}

	return false
}

// SameAddress reports whether two locations name the same multiplexer pane at
// the same place, ignoring display names, paths, processes, and terminals.
func (l Location) SameAddress(other Location) bool {
	l.WindowName, other.WindowName = "", ""
	l.TabName, other.TabName = "", ""
	l.WorkspaceName, other.WorkspaceName = "", ""
	l.PaneCurrentPath, other.PaneCurrentPath = "", ""
	l.PanePID, other.PanePID = 0, 0
	l.PaneTTY, other.PaneTTY = "", ""
	l.ClientTTY, other.ClientTTY = "", ""

	return l == other
}

func (l Location) Empty() bool {
	var empty Location
	return l == empty
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

// ActivityAliases maps every lowercase name NormalizeActivity accepts to its
// activity.
func ActivityAliases() map[string]Activity {
	return map[string]Activity{
		string(ActivityRunning):     ActivityRunning,
		"working":                   ActivityRunning,
		"busy":                      ActivityRunning,
		"retry":                     ActivityRunning,
		string(ActivityWaiting):     ActivityWaiting,
		"blocked":                   ActivityWaiting,
		string(ActivityIdle):        ActivityIdle,
		"offline":                   ActivityIdle,
		string(ActivityFailed):      ActivityFailed,
		"error":                     ActivityFailed,
		"errored":                   ActivityFailed,
		"crash":                     ActivityFailed,
		"crashed":                   ActivityFailed,
		string(ActivityInterrupted): ActivityInterrupted,
		"paused":                    ActivityInterrupted,
		"stopped":                   ActivityInterrupted,
		"canceled":                  ActivityInterrupted,
		"cancelled":                 ActivityInterrupted,
		string(ActivityUnknown):     ActivityUnknown,
	}
}

func NormalizeActivity(value string) (Activity, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return "", nil
	}

	activity, ok := ActivityAliases()[normalized]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownActivity, value)
	}

	return activity, nil
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
