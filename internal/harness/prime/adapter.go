package prime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/command"
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	primeAgentCommand       = "prime-agent"
	primeIntegrationSource  = "prime-agent-catalog"
	primeIntegrationVersion = 1
	primeCatalogTimeout     = 5 * time.Second
	primeCatalogOutputLimit = 4 << 20
	primeNativeCatalogLabel = "native session catalog"
)

var (
	errCatalogSessionsMissing  = errors.New("prime agent session catalog is missing sessions array")
	errCatalogSessionIDMissing = errors.New("prime agent catalog record is missing durable sessionId")
	errDaemonNotRunning        = errors.New("prime agent daemon socket not found")
)

type primeHarness struct{ harness.BaseAdapter }

type catalogDocument struct {
	Sessions []catalogRecord `json:"sessions"`
}

//nolint:tagliatelle // Prime Agent's public catalog schema uses camelCase fields.
type catalogRecord struct {
	ID                    string `json:"id"`
	ActiveSessionID       string `json:"activeSessionId"`
	SessionID             string `json:"sessionId"`
	SessionFile           string `json:"sessionFile"`
	SessionName           string `json:"sessionName"`
	FirstMessage          string `json:"firstMessage"`
	CWD                   string `json:"cwd"`
	Activity              string `json:"activity"`
	Lifecycle             string `json:"lifecycle"`
	RuntimeKind           string `json:"runtimeKind"`
	WorkerPID             int    `json:"workerPid"`
	IsQuotaParked         bool   `json:"isQuotaParked"`
	RLMDepth              int    `json:"rlmDepth"`
	ParentSessionPath     string `json:"parentSessionPath"`
	ParentSessionID       string `json:"parentSessionId"`
	ParentActiveSessionID string `json:"parentActiveSessionId"`
	RLMChildID            string `json:"rlmChildId"`
}

func New() primeHarness {
	return primeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: false,
		CatalogCreates:   false,
		ID:               registry.Harness("prime-agent"),
		Aliases:          []string{"prime", "primeagent", "prime_agent"},
		ProcessNames:     []string{primeAgentCommand},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: []string{"PRIME_AGENT_INTERNAL_DAEMON_WORKER_CWD"},
			PID:         nil,
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart:      false,
			SessionEnd:        false,
			RunningIdle:       true,
			WaitingPermission: false,
			ProcessIdentity:   true,
			NativeCatalog:     true,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: primeIntegrationVersion,
		IntegrationSource:  primeIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (primeHarness) InstallPlan(string) (harness.InstallPlan, error) {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.NativeCatalogAction{
		Label:   primeNativeCatalogLabel,
		Command: []string{primeAgentCommand, "sessions", "--all", "--json"},
	}}}, nil
}

func (primeHarness) NoInstallRequired() {}

func (primeHarness) ResumeCommand(sessionID, sessionPath string) []string {
	if sessionPath != "" {
		return []string{primeAgentCommand, harness.ResumeFlag, sessionPath}
	}
	if sessionID != "" {
		return []string{primeAgentCommand, harness.ResumeFlag, sessionID}
	}
	return nil
}

func (primeHarness) ObservableProcess(process processinfo.Process) bool {
	return filepath.Base(process.Executable) == primeAgentCommand &&
		len(process.Args) > 1 && process.Args[1] == "worker"
}

func (primeHarness) Catalog(ctx context.Context) ([]harness.CatalogSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read Prime Agent session catalog: %w", err)
	}
	binary, err := exec.LookPath(primeAgentCommand)
	if err != nil {
		return nil, errors.Join(harness.ErrCatalogUnavailable, fmt.Errorf("resolve %s: %w", primeAgentCommand, err))
	}
	if !daemonSocketExists() {
		return nil, errors.Join(harness.ErrCatalogUnavailable, errDaemonNotRunning)
	}
	output, err := command.RunWithLimits(ctx, primeCatalogTimeout, primeCatalogOutputLimit, binary, nil, "sessions", "--all", "--json")
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("read Prime Agent session catalog: %w", ctx.Err())
		}
		return nil, fmt.Errorf("read Prime Agent session catalog: %w", err)
	}
	return parseCatalog(output)
}

func parseCatalog(data []byte) ([]harness.CatalogSession, error) {
	var document catalogDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode Prime Agent session catalog: %w", err)
	}
	if document.Sessions == nil {
		return nil, errCatalogSessionsMissing
	}
	sessions := make([]harness.CatalogSession, 0, len(document.Sessions))
	var failures []error
	for index, record := range document.Sessions {
		session, err := record.catalogSession()
		if err != nil {
			failures = append(failures, fmt.Errorf("decode Prime Agent session catalog record %d: %w", index+1, err))
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, errors.Join(failures...)
}

func (record catalogRecord) catalogSession() (harness.CatalogSession, error) {
	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		return harness.CatalogSession{}, errCatalogSessionIDMissing
	}
	sessionPath := strings.TrimSpace(record.SessionFile)
	cwd := strings.TrimSpace(record.CWD)
	activity := primeActivity(record.Activity)
	processPID := record.WorkerPID
	current := processPID > 0
	if !current {
		processPID = 0
		activity = ""
	} else if record.IsQuotaParked {
		activity = registry.ActivityFailed
	}
	return harness.CatalogSession{
		SessionID:     sessionID,
		SessionPath:   sessionPath,
		ResumeCommand: New().ResumeCommand(sessionID, sessionPath),
		CWD:           cwd,
		ProjectRoot:   cwd,
		ProcessPID:    processPID,
		Current:       current,
		Activity:      activity,
		Attributes:    record.attributes(),
		Title:         strings.TrimSpace(record.SessionName),
	}, nil
}

func primeActivity(value string) registry.Activity {
	switch strings.TrimSpace(value) {
	case "idle":
		return registry.ActivityIdle
	case "working":
		return registry.ActivityRunning
	default:
		return ""
	}
}

func (record catalogRecord) attributes() map[string]string {
	attributes := make(map[string]string)
	add := func(key, value string) {
		if value = strings.TrimSpace(value); value != "" {
			attributes[key] = value
		}
	}
	add("prime_active_session_id", record.ActiveSessionID)
	if record.ID != record.ActiveSessionID {
		add("prime_catalog_id", record.ID)
	}
	add("prime_lifecycle", record.Lifecycle)
	add("prime_runtime_kind", record.RuntimeKind)
	if record.IsQuotaParked {
		attributes["prime_quota_parked"] = "true"
	}
	add("prime_session_name", record.SessionName)
	add("prime_first_message", record.FirstMessage)
	add("prime_parent_session_path", record.ParentSessionPath)
	add("prime_parent_session_id", record.ParentSessionID)
	add("prime_parent_active_session_id", record.ParentActiveSessionID)
	add("prime_rlm_child_id", record.RLMChildID)
	if record.RLMDepth > 0 {
		attributes["prime_rlm_depth"] = strconv.Itoa(record.RLMDepth)
	}
	return attributes
}

func daemonSocketExists() bool {
	for _, path := range daemonSocketPaths() {
		if info, err := os.Stat(path); err == nil && info.Mode().Type() == os.ModeSocket {
			return true
		}
	}
	return false
}

// The launcher pins a Rust-only socket unless PRIME_AGENT_DAEMON_SOCKET is set;
// the bare binary and the legacy TypeScript daemon use prime-agent-<uid>.
func daemonSocketPaths() []string {
	if value := strings.TrimSpace(os.Getenv("PRIME_AGENT_DAEMON_SOCKET")); value != "" {
		return []string{expandHome(value, harness.HomeDir())}
	}
	uid := strconv.Itoa(os.Getuid())
	tmp := firstEnv("TMPDIR")
	legacyTmp := firstEnv("TMPDIR", "TMP", "TEMP")
	return []string{
		filepath.Join(tmp, "prime-agent-rust-"+uid, "daemon.sock"),
		filepath.Join(tmp, "prime-agent-"+uid, "daemon.sock"),
		filepath.Join(legacyTmp, "prime-agent-"+uid, "daemon.sock"),
	}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return "/tmp"
}

func primeAgentDir() string {
	return primeAgentDirUnder(harness.HomeDir())
}

func primeAgentDirUnder(home string) string {
	if value := strings.TrimSpace(os.Getenv("PRIME_AGENT_CODING_AGENT_DIR")); value != "" {
		return expandHome(value, home)
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".prime", "agent")
}

func primeSessionsDir(home string) string {
	if value := strings.TrimSpace(os.Getenv("PRIME_AGENT_SESSION_DIR")); value != "" {
		return expandHome(value, home)
	}
	if agentDir := primeAgentDirUnder(home); agentDir != "" {
		return filepath.Join(agentDir, "sessions")
	}
	return ""
}

func expandHome(path, home string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	if home == "" {
		return ""
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}
