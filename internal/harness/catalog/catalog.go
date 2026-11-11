package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/agy"
	"github.com/zigai/aht/v2/internal/harness/amp"
	"github.com/zigai/aht/v2/internal/harness/claude"
	"github.com/zigai/aht/v2/internal/harness/cline"
	"github.com/zigai/aht/v2/internal/harness/codex"
	"github.com/zigai/aht/v2/internal/harness/copilot"
	"github.com/zigai/aht/v2/internal/harness/crush"
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
	"github.com/zigai/aht/v2/internal/harness/prime"
	"github.com/zigai/aht/v2/internal/harness/qwen"
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
	crush.New(),
	qwen.New(),
	prime.New(),
}

var adaptersByID = func() map[registry.Harness]harness.Adapter {
	byID := make(map[registry.Harness]harness.Adapter, len(adapters))
	for _, adapter := range adapters {
		id := adapter.Definition().ID
		if _, exists := byID[id]; exists {
			panic(fmt.Sprintf("duplicate harness ID %q", id))
		}

		byID[id] = adapter
	}

	return byID
}()

var harnessesByCommand = func() map[string]registry.Harness {
	byCommand := make(map[string]registry.Harness, len(adapters))
	for _, adapter := range adapters {
		definition := adapter.Definition()
		for _, name := range definition.ProcessNames {
			normalized := normalizeToken(name)
			if _, exists := byCommand[normalized]; !exists {
				byCommand[normalized] = definition.ID
			}
		}
	}

	return byCommand
}()

// NativeCatalogEntry associates one native catalog row with the adapter that
// supplied it. Reporter identity comes from the adapter definition rather than
// provider output.
type NativeCatalogEntry struct {
	Harness  registry.Harness
	Reporter registry.Reporter
	Session  harness.CatalogSession
}

func All() []harness.Adapter {
	return append([]harness.Adapter(nil), adapters...)
}

// NativeCatalog lists sessions from every adapter with a native provider.
// Successful and partial provider results are retained when another provider
// fails. An unavailable provider is expected on hosts that do not run it and is
// therefore omitted without degrading the result.
func NativeCatalog(ctx context.Context) ([]NativeCatalogEntry, error) {
	return nativeCatalog(ctx, adapters)
}

func Find(harnessID registry.Harness) (harness.Adapter, bool) {
	adapter, ok := adaptersByID[harnessID]
	return adapter, ok
}

func SupportsScreen(harnessID registry.Harness) bool {
	adapter, ok := Find(harnessID)
	if !ok {
		return false
	}

	return adapter.Definition().ScreenManifest != ""
}

func ProcessFilterFor(harnessID registry.Harness) (harness.ProcessFilter, bool) {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil, false
	}

	filter, ok := adapter.(harness.ProcessFilter)

	return filter, ok
}

// IntegrationVersionFor returns the generated-artifact version of a harness,
// or zero when the harness is unknown.
func IntegrationVersionFor(harnessID registry.Harness) int {
	adapter, ok := Find(harnessID)
	if !ok {
		return 0
	}

	return adapter.Definition().IntegrationVersion
}

func Parse(value string) (registry.Harness, error) {
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

	return "", fmt.Errorf("%w %q", registry.ErrUnknownHarness, value)
}

func EnvNames(harnessID registry.Harness, field harness.EnvField) []string {
	if adapter, ok := Find(harnessID); ok {
		return slices.Clone(envNamesForField(adapter.Definition().Env, field))
	}

	return nil
}

func FromCommand(command string) (registry.Harness, bool) {
	harnessID, ok := harnessesByCommand[normalizeToken(filepath.Base(command))]
	return harnessID, ok
}

func ProcessNames(harnessID registry.Harness) []string {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil
	}

	return adapter.Definition().ProcessNames
}

func PayloadDefaults(
	harnessID registry.Harness,
	rawPayload json.RawMessage,
) (harness.PayloadDefaults, error) {
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

func PayloadCompatible(harnessID registry.Harness, rawPayload json.RawMessage) bool {
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

	return adapter.ResumeCommand(sessionID, sessionPath)
}

// LocationsFor lists the documented configuration locations of a harness. It
// reports false when the harness is unknown.
func LocationsFor(harnessID registry.Harness, projectDir string) ([]harness.Location, bool) {
	adapter, ok := Find(harnessID)
	if !ok {
		return nil, false
	}

	return adapter.Locations(projectDir), true
}

func WithResumeCommand(observation registry.Observation) registry.Observation {
	switch observation.Evidence.(type) {
	case *registry.Report, *registry.Listing:
	case *registry.Sighting, *registry.Placement, *registry.Reading, *registry.Verification, nil:
		return observation
	}

	if observation.Listing() != nil && len(observation.Listing().ResumeCommand) > 0 {
		return observation
	}

	command := ResumeCommandFor(
		observation.Harness,
		observation.Subject.SessionID,
		observation.Subject.SessionPath,
	)
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
	ctx context.Context,
	harnessID registry.Harness,
	explicitEvent string,
	rawPayload json.RawMessage,
	payload map[string]any,
	parentArgs []string,
) (harness.HookResult, bool, error) {
	adapter, ok := Find(harnessID)
	if !ok {
		return emptyHookResult, false, nil
	}

	hookAdapter, ok := adapter.(harness.HookAdapter)
	if !ok {
		return emptyHookResult, false, nil
	}

	result, err := hookAdapter.HandleHook(ctx, harness.HookInvocation{
		Event:      explicitEvent,
		RawPayload: rawPayload,
		Payload:    payload,
		ParentArgs: parentArgs,
	})
	if result.Response == nil {
		result.Response = map[string]any{}
	}

	if err != nil {
		return result, true, fmt.Errorf("handle %s hook: %w", harnessID, err)
	}

	return result, true, nil
}

func LifecycleFor(
	id registry.Harness,
	event string,
	attributes map[string]string,
) harness.LifecycleDefaults {
	if adapter, ok := Find(id); ok {
		return adapter.LifecycleDefaults(event, attributes)
	}

	return harness.TranslateLifecycle(event, "")
}

func PrepareObservation(observation registry.Observation) registry.Observation {
	report := observation.Report()
	if report == nil {
		return observation
	}

	defaults := LifecycleFor(observation.Harness, report.Event, report.Attributes)

	report.Event = defaults.Event
	if report.Lifecycle == nil && defaults.Lifecycle != "" {
		report.Lifecycle = &defaults.Lifecycle
	}

	if report.Claim == nil && defaults.Presence != "" {
		report.Claim = &defaults.Presence
	}

	prepareDetail(observation)

	return WithResumeCommand(observation)
}

// ProgramStatusApp returns the app name the harness reports in its OSC 7501
// records, or "" when its records are not used.
func ProgramStatusApp(id registry.Harness) string {
	if adapter, ok := Find(id); ok {
		return adapter.Definition().ProgramStatusApp
	}

	return ""
}

func DetailCapabilitiesFor(id registry.Harness) registry.DetailCapabilities {
	if adapter, ok := Find(id); ok {
		return adapter.Definition().DetailCapabilities
	}

	return registry.DetailCapabilities{
		Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
	}
}

func PayloadOmissions(harnessID registry.Harness) []string {
	if adapter, ok := Find(harnessID); ok {
		return slices.Clone(adapter.Definition().PayloadOmissions)
	}

	return nil
}

func nativeCatalog(ctx context.Context, sources []harness.Adapter) ([]NativeCatalogEntry, error) {
	var (
		entries  []NativeCatalogEntry
		failures []error
	)

	for _, adapter := range sources {
		if err := ctx.Err(); err != nil {
			failures = append(failures, fmt.Errorf("list native catalogs: %w", err))
			break
		}

		provider, ok := adapter.(harness.CatalogProvider)
		if !ok {
			continue
		}

		definition := adapter.Definition()
		sessions, err := provider.Catalog(ctx)
		reporter := registry.Reporter{
			Sequence: nil, Integration: definition.IntegrationSource,
			Version: definition.IntegrationVersion, MultiSession: !definition.ExclusiveProcess,
		}

		for _, session := range sessions {
			session.ResumeCommand = append([]string(nil), session.ResumeCommand...)
			session.Attributes = cloneCatalogAttributes(session.Attributes)
			entries = append(
				entries,
				NativeCatalogEntry{Harness: definition.ID, Reporter: reporter, Session: session},
			)
		}

		if err != nil && !errors.Is(err, harness.ErrCatalogUnavailable) {
			failures = append(
				failures,
				fmt.Errorf("list %s native catalog: %w", definition.ID, err),
			)
		}
	}

	return entries, errors.Join(failures...)
}

func cloneCatalogAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}

	cloned := make(map[string]string, len(attributes))
	maps.Copy(cloned, attributes)

	return cloned
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

func prepareDetail(observation registry.Observation) {
	report := observation.Report()
	if report.Detail != nil || report.Activity == nil {
		return
	}

	adapter, ok := Find(observation.Harness)
	if !ok {
		return
	}

	if detailer, ok := adapter.(harness.ActivityDetailer); ok {
		report.Detail = detailer.ActivityDetail(
			report.Event,
			*report.Activity,
			report.Attributes,
		)
	}
}
