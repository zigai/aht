package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/shlex"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	ManagedMarker      = "aht managed integration"
	HookTimeoutSeconds = 5
	HookTypeCommand    = "command"
	ResumeFlag         = "--resume"

	HookActivityRunning     HookTransition = "activity:running"
	HookActivityWaiting     HookTransition = "activity:waiting"
	HookActivityIdle        HookTransition = "activity:idle"
	HookActivityFailed      HookTransition = "activity:failed"
	HookActivityInterrupted HookTransition = "activity:interrupted"
	HookPresenceLive        HookTransition = "presence:live"
	HookPresenceGone        HookTransition = "presence:gone"
)

const (
	ReportStdinRaw      ReportStdin = "--raw-stdin"
	ReportStdinDefaults ReportStdin = "--raw-stdin-defaults-only"

	nativeHookWords = 6
)

const (
	PluginRegistrationMissing PluginRegistrationState = iota
	PluginRegistrationCurrent
	PluginRegistrationStale
	PluginRegistrationDisabled
	PluginRegistrationForeign
)

var ErrHookConfigShape = errors.New("hook config has an unexpected shape")

// ReportStdin selects how a report command reads the native hook payload.
type ReportStdin string

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

type JSONMergeAction struct {
	Plan JSONMergeInstallPlan
}

type ManagedTextBlockAction struct {
	Plan ManagedTextBlockInstallPlan
}

type RenderedFileAction struct {
	Plan RenderedFileInstallPlan
}

type PluginDirectoryAction struct {
	Plan PluginDirectoryInstallPlan
}

// NativeCatalogAction labels integration data already exposed by a native
// catalog. Integration management never installs an artifact for it.
type NativeCatalogAction struct {
	Label string
}

type ShimAction struct{}

//exhaustruct:optional
type JSONCommandHookInstallPlan struct {
	Path          string
	Source        string
	Label         string
	ConfigLabel   string
	StatusMessage string
	HooksKey      string
	Hooks         []CommandHookInstallSpec
	Trust         JSONCommandHookTrust
}

type JSONCommandHookTrust interface {
	Install(ctx context.Context) error
	Inspect(ctx context.Context) (bool, error)
}

//exhaustruct:optional
type CommandHookInstallSpec struct {
	Event   string
	Matcher string
	Command string
	// Timeout is in seconds; zero means HookTimeoutSeconds.
	Timeout int
}

//exhaustruct:optional
type JSONMergeInstallPlan struct {
	Path        string
	Source      string
	Label       string
	ConfigLabel string
	Merge       func(map[string]any, func(string) bool) (bool, error)
	Remove      func(map[string]any, func(string) bool) bool
}

type ManagedTextBlockInstallPlan struct {
	Path        string
	Label       string
	ConfigLabel string
	StartMarker string
	EndMarker   string
	Block       string
}

//exhaustruct:optional
type RenderedFileInstallPlan struct {
	Path        string
	Label       string
	ConfigLabel string
	Content     string
	JSONContent any
}

//exhaustruct:optional
type RenderedFileInstallSpec struct {
	Name        string
	Content     string
	JSONContent any
}

// PluginDirectoryInstallPlan installs Files, in order, into Dir and writes the
// identity to ArtifactMarkerFile beside them.
//
//exhaustruct:optional
type PluginDirectoryInstallPlan struct {
	Dir            string
	Label          string
	Files          []RenderedFileInstallSpec
	Identity       ArtifactIdentity
	ImportManifest *ImportManifestInstallPlan
	Registration   PluginRegistration
	// Retired lists former locations of this plugin. Installing or removing
	// the plugin also removes aht-managed artifacts found there.
	Retired []PluginDirectoryInstallPlan
}
type PluginRegistrationState uint8

type PluginRegistration interface {
	ID() string
	Label() string
	Inspect(ctx context.Context, pluginDir string) (PluginRegistrationState, error)
	EnsureMutable(pluginDir string) error
	Install(ctx context.Context, pluginDir string) error
	CleanupFailedInstall(
		ctx context.Context,
		previousState PluginRegistrationState,
		pluginDir string,
	) error
	Remove(ctx context.Context, pluginDir string) error
}

type ImportManifestInstallPlan struct {
	Path       string
	Name       string
	Source     string
	Components []string
}

// NativeCatalog returns the first catalog when every action is a native catalog.
func (plan InstallPlan) NativeCatalog() (NativeCatalogAction, bool) {
	var first NativeCatalogAction

	for index, action := range plan.Actions {
		native, ok := action.(NativeCatalogAction)
		if !ok {
			return NativeCatalogAction{Label: ""}, false
		}

		if index == 0 {
			first = native
		}
	}

	return first, len(plan.Actions) > 0
}

func (JSONCommandHooksAction) installAction() {}

func (JSONMergeAction) installAction() {}

func (ManagedTextBlockAction) installAction() {}

func (RenderedFileAction) installAction() {}

func (PluginDirectoryAction) installAction() {}

func (NativeCatalogAction) installAction() {}

func (ShimAction) installAction() {}

// ReportHookCommand renders the shell command a generated hook runs to report
// a transition for the harness the definition describes.
func ReportHookCommand[T Transition](
	binary string,
	definition Definition,
	stdin ReportStdin,
	transition T,
	event string,
) string {
	flag, value := hookTransitionArgument(transition)

	quotedEvent := ""
	if event != "" {
		quotedEvent = ShellQuote(event)
	}

	return ReportScriptCommand(binary, definition, stdin, flag, ShellQuote(value), quotedEvent)
}

// ReportScriptCommand renders a report command whose transition value and
// event are already shell words, such as quoted variable expansions. flag is
// "--activity" or "--presence"; an empty event omits --event.
func ReportScriptCommand(
	binary string,
	definition Definition,
	stdin ReportStdin,
	flag string,
	value string,
	event string,
) string {
	parts := []string{ShellQuote(binary), "report", ShellQuote(string(definition.ID)), flag, value}
	if event != "" {
		parts = append(parts, "--event", event)
	}

	parts = append(
		parts,
		"--reporter-version", ShellQuote(strconv.Itoa(definition.IntegrationVersion)),
		"--reporter", ShellQuote(definition.IntegrationSource),
	)
	if !definition.ExclusiveProcess {
		parts = append(parts, "--multi-session")
	}

	return strings.Join(append(parts, string(stdin), "--quiet"), " ")
}

// NativeHookCommand renders the command a generated hook runs to pass a native
// hook event to the adapter's HookAdapter.
func NativeHookCommand(binary string, id registry.Harness, event string) string {
	return strings.Join([]string{
		ShellQuote(binary), "--json", "hook", ShellQuote(string(id)), "--event", ShellQuote(event),
	}, " ")
}

// ParseNativeHookCommand returns the binary of a command NativeHookCommand
// rendered for id. It reports false for any other command.
func ParseNativeHookCommand(command string, id registry.Harness) (string, bool) {
	words, err := shlex.Split(command)
	if err != nil || len(words) != nativeHookWords || words[1] != "--json" ||
		words[2] != "hook" || words[3] != string(id) || words[4] != "--event" || words[5] == "" {
		return "", false
	}

	if command != NativeHookCommand(words[0], id, words[5]) {
		return "", false
	}

	return words[0], true
}

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

func ScriptString(value string) string {
	var encoded strings.Builder

	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(value); err != nil {
		panic(fmt.Errorf("encode script string: %w", err))
	}

	return strings.TrimSuffix(encoded.String(), "\n")
}

// RenderScriptTemplate fills a plugin or extension template with the binary
// and the identity of the definition.
func RenderScriptTemplate(template string, binary string, definition Definition) string {
	rendered := strings.NewReplacer(
		"{{MANAGED_MARKER}}", ManagedMarker,
		"{{INTEGRATION_ID}}", string(definition.ID),
		"{{INTEGRATION_VERSION}}", strconv.Itoa(definition.IntegrationVersion),
		"{{BINARY}}", ScriptString(binary),
		"{{SOURCE}}", strconv.Quote(definition.IntegrationSource),
		"{{TYPESCRIPT_QUEUE}}", typeScriptQueueTemplate,
		"{{TYPESCRIPT_EVENT_PARSER}}", typeScriptEventParserTemplate,
	).Replace(template)

	// The replacer does not rescan the event parser it splices in, so the
	// alias table it references is filled in a second pass.
	return strings.ReplaceAll(rendered, "{{ACTIVITY_ALIASES}}", typeScriptActivityAliases())
}

func typeScriptActivityAliases() string {
	aliases := registry.ActivityAliases()
	// Plugins report no state for an unknown status.
	delete(aliases, string(registry.ActivityUnknown))

	encoded, err := json.Marshal(aliases)
	if err != nil {
		panic(fmt.Errorf("encode activity aliases: %w", err))
	}

	return string(encoded)
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
