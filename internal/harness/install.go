package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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

type JSONCommandHookAction struct {
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
	// TimeoutSeconds is in seconds; zero means HookTimeoutSeconds.
	TimeoutSeconds int
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
	Dir           string
	Label         string
	Files         []RenderedFileInstallSpec
	Identity      ArtifactIdentity
	Registration  PluginRegistration
	RequireMarker bool
	// StageRemoval moves managed files aside before removing registration.
	// Use it when unregistering does not need to open the plugin directory.
	StageRemoval bool
	// Retired lists former locations of this plugin. Installing or removing
	// the plugin also removes aht-managed artifacts found there.
	Retired []PluginDirectoryInstallPlan
	// UpgradeRegisters lets upgrade register a stale registration again. Set
	// it when registering only restores what an earlier install set up.
	UpgradeRegisters bool
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

func (JSONCommandHookAction) installAction() {}

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
	return shellCommand(binary, ReportHookArgs(definition, stdin, transition, event))
}

// ReportHookArgs returns the arguments after the binary of the report command
// ReportHookCommand renders.
func ReportHookArgs[T Transition](
	definition Definition,
	stdin ReportStdin,
	transition T,
	event string,
) []string {
	flag, value := hookTransitionArgument(transition)

	args := []string{"report", string(definition.ID), flag, value}
	if event != "" {
		args = append(args, "--event", event)
	}

	args = append(
		args,
		"--reporter-version", strconv.Itoa(definition.IntegrationVersion),
		"--reporter", definition.IntegrationSource,
	)
	if !definition.ExclusiveProcess {
		args = append(args, "--multi-session")
	}

	return append(args, string(stdin), "--quiet")
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

// IsManagedSourceCommand reports whether a direct report invocation carries
// both the requested source and evidence that aht generated it.
func IsManagedSourceCommand(command string, source string) bool {
	words, err := shlex.Split(command)
	if err != nil || len(words) < 4 || words[0] == "" || words[1] != "report" || source == "" {
		return false
	}

	target := strings.TrimSuffix(source, "-hook")
	if words[2] == target {
		return managedReportArgs(words[3:], source, ahtExecutable(words[0]))
	}

	// Older versions specified the target as --harness instead of a
	// positional argument, and identified the reporter with --source.
	if words[2] == "--harness" && words[3] == target {
		return managedReportArgs(words[4:], source, ahtExecutable(words[0]))
	}

	return false
}

// NativeHookCommand renders the command a generated hook runs to pass a native
// hook event to the adapter's HookAdapter.
func NativeHookCommand(binary string, id registry.Harness, event string) string {
	return shellCommand(binary, NativeHookArgs(id, event))
}

// NativeHookArgs returns the arguments after the binary of the command
// NativeHookCommand renders.
func NativeHookArgs(id registry.Harness, event string) []string {
	return []string{"--json", "hook", string(id), "--event", event}
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

func ahtExecutable(binary string) bool {
	switch filepath.Base(binary) {
	case "aht", "aht.exe", "old-aht", "agent-sessions", "agent-sessions.exe":
		return true
	default:
		return false
	}
}

func managedReportArgs(args []string, source string, knownBinary bool) bool {
	var sourceMatches, versioned, readsHookInput, hasTransition bool

	for leadingReportFlag(args) {
		var flag, value string

		flag, value, args = takeReportArg(args)

		switch flag {
		case "--reporter", "--source":
			sourceMatches = value == source
		case "--reporter-version":
			versioned = positiveReportVersion(value)
		case string(ReportStdinRaw), string(ReportStdinDefaults):
			readsHookInput = true
		case "--activity", "--presence", "--state":
			hasTransition = value != ""
		case "--attribute":
			if isManagedIntegrationAttribute(value, source) {
				return true
			}
		}
	}

	// An explicitly configured (possibly renamed) aht binary need not have
	// a recognizable filename. Match the generated transition, input mode,
	// and reporter version in addition to the harness and source.
	generated := versioned && readsHookInput && hasTransition

	return sourceMatches && (knownBinary || generated)
}

func leadingReportFlag(args []string) bool {
	return len(args) > 0 && strings.HasPrefix(args[0], "--") && args[0] != "--"
}

func takeReportArg(args []string) (string, string, []string) {
	flag, value, inline := strings.Cut(args[0], "=")
	args = args[1:]

	if !inline && len(args) > 0 && reportArgumentValue(args[0]) {
		return flag, args[0], args[1:]
	}

	return flag, value, args
}

func reportArgumentValue(word string) bool {
	// Generated commands may append redirections or a shell suffix, but
	// their report metadata always belongs to the leading invocation.
	return !strings.HasPrefix(word, "--") &&
		(word == "" || !strings.ContainsRune(";&|<>", rune(word[0])))
}

func positiveReportVersion(value string) bool {
	version, err := strconv.Atoi(value)

	return err == nil && version > 0
}

func isManagedIntegrationAttribute(value string, source string) bool {
	key, got, ok := strings.Cut(value, "=")
	if !ok || got != source {
		return false
	}

	suffix, ok := strings.CutPrefix(key, "aht")

	return ok && (suffix == "_integration" || suffix == "-integration" || suffix == "integration")
}

func shellCommand(binary string, args []string) string {
	words := make([]string, 0, len(args)+1)
	words = append(words, ShellQuote(binary))

	for _, arg := range args {
		words = append(words, ShellQuote(arg))
	}

	return strings.Join(words, " ")
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
	normalized, err := registry.ParseActivity(string(value))
	if err != nil || normalized == "" || normalized != value {
		panic("invalid hook activity transition")
	}

	return "--activity", string(value)
}

func presenceTransitionArgument(value registry.Presence) (string, string) {
	normalized, err := registry.ParsePresence(string(value))
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
