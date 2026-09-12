package harness

import (
	"context"
	"strconv"
	"strings"

	"github.com/zigai/aht/pkg/registry"
)

const (
	ManagedMarker                 = "aht managed integration"
	HookTimeoutSeconds            = 5
	codexSessionEndTimeoutSeconds = 3
	HookTypeCommand               = "command"
	HookEventSessionStart         = "SessionStart"
	HookEventSessionEnd           = "SessionEnd"
	HookEventUserPromptSubmit     = "UserPromptSubmit"
	HookEventPostToolUse          = "PostToolUse"
	HookEventPostToolUseFailure   = "PostToolUseFailure"
	HookEventPreToolUse           = "PreToolUse"
	HookEventStop                 = "Stop"
	ResumeFlag                    = "--resume"

	HookActivityRunning     HookTransition = "activity:running"
	HookActivityWaiting     HookTransition = "activity:waiting"
	HookActivityIdle        HookTransition = "activity:idle"
	HookActivityFailed      HookTransition = "activity:failed"
	HookActivityInterrupted HookTransition = "activity:interrupted"
	HookPresenceLive        HookTransition = "presence:live"
	HookPresenceGone        HookTransition = "presence:gone"
)

const (
	PluginRegistrationMissing PluginRegistrationState = iota
	PluginRegistrationCurrent
	PluginRegistrationStale
	PluginRegistrationForeign
)

// HookTransition is the closed, dimension-aware state used by generated hook
// specifications that need to store a transition before rendering it.
type HookTransition string

type Transition interface {
	registry.Activity | registry.Presence | HookTransition
}

type InstallPlan struct {
	Actions []InstallAction
}

type InstallAction interface {
	installAction()
}

type JSONCommandHooksAction struct {
	Plan JSONCommandHookInstallPlan
}

type CursorJSONHooksAction struct {
	Plan CursorJSONHookInstallPlan
}

type ManagedTextBlockAction struct {
	Plan ManagedTextBlockInstallPlan
}

type RenderedFileAction struct {
	Plan RenderedFileInstallPlan
}

type RenderedFilesAction struct {
	Plan RenderedFilesInstallPlan
}

type PluginDirectoryAction struct {
	Plan PluginDirectoryInstallPlan
}

type ShimAction struct{}

type JSONCommandHookInstallPlan struct {
	Path              string
	Source            string
	Label             string
	ConfigLabel       string
	StatusMessage     string
	OmitStatusMessage bool
	HooksAtRoot       bool
	Hooks             []CommandHookInstallSpec
}

type CommandHookInstallSpec struct {
	Event   string
	Matcher string
	Command string
}

type CursorJSONHookInstallPlan struct {
	Path        string
	Source      string
	Label       string
	ConfigLabel string
	Hooks       []CursorCommandHookInstallSpec
}

type CursorCommandHookInstallSpec struct {
	Event   string
	Command string
}

type ManagedTextBlockInstallPlan struct {
	Path        string
	Label       string
	ConfigLabel string
	StartMarker string
	EndMarker   string
	Block       string
}

type RenderedFileInstallPlan struct {
	Path        string
	Label       string
	ConfigLabel string
	Content     string
	JSONContent any
}

type RenderedFilesInstallPlan struct {
	Dir          string
	Label        string
	ConfigLabel  string
	Files        []RenderedFileInstallSpec
	SnippetOrder []string
}

type RenderedFileInstallSpec struct {
	Name        string
	Content     string
	JSONContent any
}

type PluginDirectoryInstallPlan struct {
	Dir            string
	Label          string
	Files          []RenderedFileInstallSpec
	SnippetOrder   []string
	MarkerFile     string
	ObsoleteFiles  []string
	ImportManifest *ImportManifestInstallPlan
	Registration   PluginRegistration
}
type PluginRegistrationState uint8

type PluginRegistration interface {
	ID() string
	Label() string
	Inspect(ctx context.Context, pluginDir string) (PluginRegistrationState, error)
	EnsureMutable(pluginDir string) error
	Install(ctx context.Context, pluginDir string) error
	CleanupFailedInstall(ctx context.Context, previousState PluginRegistrationState, pluginDir string) error
	Remove(ctx context.Context, pluginDir string) error
}

type ImportManifestInstallPlan struct {
	Path       string
	Name       string
	Source     string
	Components []string
}

func (JSONCommandHooksAction) installAction() {}

func (CursorJSONHooksAction) installAction() {}

func (ManagedTextBlockAction) installAction() {}

func (RenderedFileAction) installAction() {}

func (RenderedFilesAction) installAction() {}

func (PluginDirectoryAction) installAction() {}

func (ShimAction) installAction() {}

// HookTimeoutSecondsFor returns the native command-hook timeout for an event.
func HookTimeoutSecondsFor(harness registry.Harness, event string) int {
	if harness == registry.HarnessCodex && event == HookEventSessionEnd {
		return codexSessionEndTimeoutSeconds
	}

	return HookTimeoutSeconds
}

func ReportHookCommand[T Transition](binary string, harness registry.Harness, transition T, event string, source string) string {
	return reportHookCommand(binary, harness, transition, event, source, "--raw-stdin")
}

func RawStdinDefaultsReportHookCommand[T Transition](
	binary string,
	harness registry.Harness,
	transition T,
	event string,
	source string,
) string {
	return reportHookCommand(binary, harness, transition, event, source, "--raw-stdin-defaults-only")
}

// State returns the activity or presence state represented by the transition.
func (transition HookTransition) State() string {
	_, state := hookTransitionArgument(transition)

	return state
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}

	if isSafeShellWord(value) {
		return value
	}

	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func RenderScriptTemplate(template string, integrationID string, binary string, source string, version int) string {
	return strings.NewReplacer(
		"{{MANAGED_MARKER}}", ManagedMarker,
		"{{INTEGRATION_ID}}", integrationID,
		"{{INTEGRATION_VERSION}}", strconv.Itoa(version),
		"{{BINARY}}", strconv.Quote(binary),
		"{{SOURCE}}", strconv.Quote(source),
		"{{TYPESCRIPT_QUEUE}}", typeScriptQueueTemplate,
	).Replace(template)
}

func reportHookCommand[T Transition](
	binary string,
	harness registry.Harness,
	transition T,
	event string,
	source string,
	stdinFlag string,
) string {
	parts := []string{
		ShellQuote(binary),
		"report",
		ShellQuote(string(harness)),
	}
	flag, value := hookTransitionArgument(transition)
	parts = append(parts, flag, ShellQuote(value))
	if event != "" {
		parts = append(parts, "--event", ShellQuote(event))
	}
	parts = append(
		parts,
		"--attribute", ShellQuote("aht_integration_version="+strconv.Itoa(IntegrationVersion)),
		"--attribute", ShellQuote("aht_integration="+source),
		stdinFlag,
		"--quiet",
	)
	return strings.Join(parts, " ")
}

func hookTransitionArgument[T Transition](transition T) (string, string) {
	switch value := any(transition).(type) {
	case registry.Activity:
		return activityTransitionArgument(value)
	case registry.Presence:
		return presenceTransitionArgument(value)
	case HookTransition:
		return storedHookTransitionArgument(value)
	default:
		panic("unsupported hook transition type")
	}
}

func activityTransitionArgument(value registry.Activity) (string, string) {
	normalized, err := registry.NormalizeActivity(string(value))
	if err != nil || normalized == "" || normalized != value {
		panic("invalid hook activity transition")
	}

	return "--activity", string(value)
}

func presenceTransitionArgument(value registry.Presence) (string, string) {
	normalized, err := registry.NormalizePresence(string(value))
	if err != nil || normalized == "" || normalized != value {
		panic("invalid hook presence transition")
	}

	return "--presence", string(value)
}

func storedHookTransitionArgument(value HookTransition) (string, string) {
	switch value {
	case HookActivityRunning:
		return "--activity", string(registry.ActivityRunning)
	case HookActivityWaiting:
		return "--activity", string(registry.ActivityWaiting)
	case HookActivityIdle:
		return "--activity", string(registry.ActivityIdle)
	case HookActivityFailed:
		return "--activity", string(registry.ActivityFailed)
	case HookActivityInterrupted:
		return "--activity", string(registry.ActivityInterrupted)
	case HookPresenceLive:
		return "--presence", string(registry.PresenceLive)
	case HookPresenceGone:
		return "--presence", string(registry.PresenceGone)
	default:
		panic("invalid stored hook transition")
	}
}

func isSafeShellWord(value string) bool {
	for _, r := range value {
		if !isSafeShellRune(r) {
			return false
		}
	}

	return true
}

func isSafeShellRune(r rune) bool {
	switch {
	case r == '/', r == '.', r == '_', r == '-', r == '+', r == ':', r == '=':
		return true
	case r >= '0' && r <= '9':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	default:
		return false
	}
}
