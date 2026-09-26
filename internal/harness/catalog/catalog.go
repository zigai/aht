package catalog

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/agy"
	"github.com/zigai/aht/v2/internal/harness/amp"
	"github.com/zigai/aht/v2/internal/harness/claude"
	"github.com/zigai/aht/v2/internal/harness/cline"
	"github.com/zigai/aht/v2/internal/harness/codex"
	"github.com/zigai/aht/v2/internal/harness/copilot"
	"github.com/zigai/aht/v2/internal/harness/cursor"
	"github.com/zigai/aht/v2/internal/harness/droid"
	"github.com/zigai/aht/v2/internal/harness/goose"
	"github.com/zigai/aht/v2/internal/harness/grok"
	"github.com/zigai/aht/v2/internal/harness/hermes"
	"github.com/zigai/aht/v2/internal/harness/kilo"
	"github.com/zigai/aht/v2/internal/harness/kimi"
	"github.com/zigai/aht/v2/internal/harness/omp"
	"github.com/zigai/aht/v2/internal/harness/openclaw"
	"github.com/zigai/aht/v2/internal/harness/opencode"
	"github.com/zigai/aht/v2/internal/harness/pi"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	emptyPayloadDefaults harness.PayloadDefaults
	emptyHookResult      harness.HookResult

	// tokenPunctuation strips separators so that names and aliases match without
	// punctuation, mirroring the documented contract of harness.Parse.
	tokenPunctuation = strings.NewReplacer(
		" ", "",
		".", "",
		"-", "",
		"_", "",
		":", "",
		"!", "",
	)
)

var adapters = []harness.Adapter{
	claude.New(),
	codex.New(),
	cursor.New(),
	copilot.New(),
	cline.New(),
	kimi.New(),
	grok.New(),
	goose.New(),
	pi.New(),
	omp.New(),
	opencode.New(),
	agy.New(),
	kilo.New(),
	droid.New(),
	openclaw.New(),
	hermes.New(),
	amp.New(),
}

func All() []harness.Adapter {
	return append([]harness.Adapter(nil), adapters...)
}

func Find(harnessID registry.Harness) (harness.Adapter, bool) {
	for _, adapter := range adapters {
		if adapter.Definition().ID == harnessID {
			return adapter, true
		}
	}
	return nil, false
}

func SupportsScreen(harnessID registry.Harness) bool {
	adapter, ok := Find(harnessID)
	if !ok {
		return false
	}
	provider, ok := adapter.(harness.ScreenManifestProvider)
	return ok && provider.ScreenManifest() != ""
}

func ProcessFilterFor(harnessID registry.Harness) (harness.ProcessFilter, bool) {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil, false
	}
	filter, ok := adapter.(harness.ProcessFilter)
	return filter, ok
}

func WireRunnerFor(harnessID registry.Harness) (harness.WireRunner, bool) {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil, false
	}
	runner, ok := adapter.(harness.WireRunner)
	return runner, ok
}

func IntegrationVersionFor(harnessID registry.Harness) int {
	adapter, ok := Find(harnessID)
	if !ok {
		return harness.IntegrationVersion
	}
	return adapter.Definition().IntegrationVersion
}

func Normalize(value string) (registry.Harness, error) {
	normalized := normalizeToken(value)
	for _, adapter := range adapters {
		definition := adapter.Definition()
		if normalized == normalizeToken(string(definition.ID)) {
			return definition.ID, nil
		}
		if containsNormalizedToken(definition.Aliases, normalized) {
			return definition.ID, nil
		}
	}
	return "", fmt.Errorf("%w: %q", registry.ErrUnknownHarness, value)
}

func SupportedNames() []string {
	names := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		names = append(names, string(adapter.Definition().ID))
	}
	return names
}

func EnvNames(field harness.EnvField) []string {
	names := genericEnvNames(field)
	for _, adapter := range adapters {
		names = appendUnique(names, envNamesForField(adapter.Definition().Env, field)...)
	}
	return names
}

func FromCommand(command string) (registry.Harness, bool) {
	normalized := normalizeToken(filepath.Base(command))
	for _, adapter := range adapters {
		definition := adapter.Definition()
		if containsNormalizedToken(definition.ProcessNames, normalized) {
			return definition.ID, true
		}
	}
	return "", false
}

func ProcessNames(harnessID registry.Harness) []string {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil
	}
	return adapter.Definition().ProcessNames
}

func DefaultsFromPayloadWithError(harnessID registry.Harness, rawPayload json.RawMessage) (harness.PayloadDefaults, error) {
	if len(rawPayload) == 0 {
		return emptyPayloadDefaults, nil
	}
	adapter, ok := Find(harnessID)
	if !ok {
		return emptyPayloadDefaults, nil
	}
	payloadAdapter, ok := adapter.(harness.PayloadAdapter)
	if !ok {
		return emptyPayloadDefaults, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return emptyPayloadDefaults, fmt.Errorf("decoding hook payload defaults: %w", err)
	}
	defaults, err := payloadAdapter.PayloadDefaults(payload)
	if err != nil {
		return emptyPayloadDefaults, fmt.Errorf("deriving harness payload defaults: %w", err)
	}
	return defaults, nil
}

// ActivityFromPayload returns the activity a report records after the harness
// refines the generated hook's declared activity from its native payload.
func ActivityFromPayload(
	harnessID registry.Harness,
	event string,
	activity registry.Activity,
	rawPayload json.RawMessage,
	at time.Time,
) (registry.Activity, error) {
	if len(rawPayload) == 0 || activity == "" {
		return activity, nil
	}
	adapter, ok := Find(harnessID)
	if !ok {
		return activity, nil
	}
	activityAdapter, ok := adapter.(harness.PayloadActivityAdapter)
	if !ok {
		return activity, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return activity, fmt.Errorf("decoding hook payload activity: %w", err)
	}
	return activityAdapter.PayloadActivity(event, activity, payload, at), nil
}

func PayloadCompatibleWithHarness(harnessID registry.Harness, rawPayload json.RawMessage) bool {
	if len(rawPayload) == 0 {
		return true
	}
	adapter, ok := Find(harnessID)
	if !ok {
		return true
	}
	payloadAdapter, ok := adapter.(harness.PayloadAdapter)
	if !ok {
		return true
	}
	return payloadAdapter.PayloadCompatible(rawPayload)
}

func ResumeCommandFor(harnessID registry.Harness, sessionID string, sessionPath string) []string {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil
	}
	resumable, ok := adapter.(harness.Resumable)
	if !ok {
		return nil
	}
	return resumable.ResumeCommand(sessionID, sessionPath)
}

func WithResumeCommand(observation registry.Observation) registry.Observation {
	switch observation.Evidence.(type) {
	case *registry.Report, *registry.Listing:
	case *registry.Sighting, *registry.Placement, *registry.Reading, nil:
		return observation
	}
	if observation.Listing() != nil && len(observation.Listing().ResumeCommand) > 0 {
		return observation
	}
	command := ResumeCommandFor(observation.Harness, observation.Subject.SessionID, observation.Subject.SessionPath)
	if len(command) == 0 {
		return observation
	}
	if observation.Listing() == nil {
		observation.SetListing(&registry.Listing{
			ResumeCommand: nil,
			CWD:           "",
			ProjectRoot:   "",
			ProcessPID:    0,
			Current:       false,
		})
	}
	observation.Listing().ResumeCommand = command
	return observation
}

func HandleHook(
	harnessID registry.Harness,
	explicitEvent string,
	rawPayload json.RawMessage,
	payload map[string]any,
	parentArgs []string,
) (harness.HookResult, bool) {
	adapter, ok := Find(harnessID)
	if !ok {
		return emptyHookResult, false
	}
	hookAdapter, ok := adapter.(harness.HookAdapter)
	if !ok {
		return emptyHookResult, false
	}
	result := hookAdapter.HandleHook(harness.HookInvocation{
		Event:      explicitEvent,
		RawPayload: rawPayload,
		Payload:    payload,
		ParentArgs: parentArgs,
	})
	if result.Response == nil {
		result.Response = map[string]any{}
	}
	return result, true
}

func LifecycleFor(id registry.Harness, event string, attributes map[string]string) harness.LifecycleDefaults {
	if adapter, ok := Find(id); ok {
		if translator, ok := adapter.(harness.LifecycleAdapter); ok {
			return translator.LifecycleDefaults(event, attributes)
		}
	}
	return harness.TranslateLifecycle(event, "")
}

func PrepareObservation(observation registry.Observation) registry.Observation {
	if observation.Kind() == "report" {
		defaults := LifecycleFor(observation.Harness, observation.Report().Event, observation.Report().Attributes)
		observation.Report().Event = defaults.Event
		if observation.Report().Lifecycle == nil && defaults.Lifecycle != "" {
			observation.Report().Lifecycle = &defaults.Lifecycle
		}
		if observation.Report().Claim == nil && defaults.Presence != "" {
			observation.Report().Claim = &defaults.Presence
		}
	}
	return WithResumeCommand(observation)
}

func HookTimeoutSecondsFor(id registry.Harness, event string) int {
	if adapter, ok := Find(id); ok {
		if policy, ok := adapter.(interface{ HookTimeout(event string) int }); ok {
			return policy.HookTimeout(event)
		}
	}
	return harness.HookTimeoutSeconds
}

func genericEnvNames(field harness.EnvField) []string {
	switch field {
	case harness.EnvSessionID:
		return []string{"AHT_SESSION_ID", "AGENT_SESSION_ID"}
	case harness.EnvSessionPath:
		return []string{"AHT_SESSION_PATH", "AGENT_SESSION_PATH"}
	case harness.EnvProjectRoot:
		return []string{"AHT_PROJECT_ROOT", "PROJECT_ROOT"}
	case harness.EnvPID:
		return []string{"AHT_PID", "AGENT_PID"}
	case harness.EnvEvent:
		return []string{"AHT_EVENT", "AGENT_EVENT"}
	default:
		return nil
	}
}

func envNamesForField(keys harness.EnvKeys, field harness.EnvField) []string {
	switch field {
	case harness.EnvSessionID:
		return keys.SessionID
	case harness.EnvSessionPath:
		return keys.SessionPath
	case harness.EnvProjectRoot:
		return keys.ProjectRoot
	case harness.EnvPID:
		return keys.PID
	case harness.EnvEvent:
		return keys.Event
	default:
		return nil
	}
}

func appendUnique(values []string, next ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(next))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range next {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func normalizeToken(value string) string {
	return tokenPunctuation.Replace(strings.ToLower(strings.TrimSpace(value)))
}

func containsNormalizedToken(values []string, normalized string) bool {
	for _, value := range values {
		if normalizeToken(value) == normalized {
			return true
		}
	}
	return false
}
