package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/registrycheck"
	"github.com/zigai/aht/v2/pkg/registry"
)

type nativeEventCase struct {
	event    string
	activity registry.Activity
	source   string
}

type nativeSequenceRunner struct {
	store      *registry.MemoryStore
	definition harness.Definition
	id         registry.Harness
	base       time.Time
	sequences  map[string]uint64
	coverage   *registrycheck.Coverage
	tick       int
	sequenced  bool
}

func TestNativeEventSequencesPreserveRegistryProperties(t *testing.T) {
	seeds, steps := uint64(2), 12
	if testing.Short() {
		seeds, steps = 1, 6
	} else if os.Getenv("AHT_EXTENDED_PARITY") != "" {
		seeds, steps = 8, 40
	}
	for _, name := range SupportedNames() {
		t.Run(name, func(t *testing.T) {
			for index := range seeds {
				seed := lifecycleSeed(name, index)
				t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
					runNativeEventInput(t, name, lifecycleBytes(seed), index%2 == 0, steps)
				})
			}
		})
	}
}

func FuzzNativeEventSequences(f *testing.F) {
	for _, name := range SupportedNames() {
		f.Add(name, lifecycleBytes(lifecycleSeed(name, 0)))
	}
	f.Fuzz(func(t *testing.T, name string, input []byte) {
		if _, err := Normalize(name); err != nil {
			return
		}
		t.Logf("input SHA-256: %x", sha256.Sum256(input))
		runNativeEventInput(t, name, input, true, 12)
	})
}

func TestNativeHookHandlersPreserveRegistryProperties(t *testing.T) {
	for _, name := range SupportedNames() {
		id, err := Normalize(name)
		if err != nil {
			t.Fatal(err)
		}
		adapter, ok := Find(id)
		if !ok {
			t.Fatalf("missing adapter %q", name)
		}
		if _, ok := adapter.(harness.HookAdapter); !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			runNativeHookAdapter(t, id)
		})
	}
}

func runNativeHookAdapter(t *testing.T, id registry.Harness) {
	t.Helper()
	store, err := registry.OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), Rules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("closing store: %v", err)
		}
	})
	payload := map[string]any{"conversationId": "hook-session", "workspacePaths": []string{"/work"}, "toolCall": map[string]any{"name": "ask_permission"}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !PayloadCompatibleWithHarness(id, raw) {
		t.Fatal("hook payload incompatible with adapter")
	}
	coverage := registrycheck.NewCoverage()
	for step, event := range []string{"PostToolUse", "PreToolUse", "PreToolUse", "PostToolUse"} {
		if applyNativeHook(t, store, id, event, step, payload, raw) {
			coverage.Hit("hook-report")
		}
	}
	if missing := coverage.Missing("hook-report"); len(missing) > 0 {
		t.Fatalf("missing hook coverage: %v", missing)
	}
}

func applyNativeHook(t *testing.T, store *registry.MemoryStore, id registry.Harness, event string, step int, payload map[string]any, raw json.RawMessage) bool {
	t.Helper()
	result, ok := HandleHook(id, event, raw, payload, nil)
	if !ok {
		t.Fatalf("hook handler missing for %q", event)
	}
	if !result.ReportOK {
		return false
	}
	observation := result.Report
	observation.At = time.Now().UTC().Add(-time.Minute).Add(time.Duration(step) * time.Millisecond)
	if _, err := store.Observe(t.Context(), PrepareObservation(observation)); err != nil {
		t.Fatalf("hook event %q: %v", event, err)
	}
	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := registrycheck.Sessions(sessions); err != nil {
		t.Fatalf("hook event %q: %v", event, err)
	}
	return true
}

func runNativeEventInput(t *testing.T, name string, input []byte, sequenced bool, steps int) {
	t.Helper()
	id, err := Normalize(name)
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := Find(id)
	if !ok {
		t.Fatalf("missing adapter %q", name)
	}
	store, err := registry.OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), Rules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("closing store: %v", err)
		}
	})
	runner := &nativeSequenceRunner{
		store: store, definition: adapter.Definition(), id: id,
		base: time.Now().UTC().Add(-time.Minute), sequenced: sequenced,
		sequences: map[string]uint64{}, coverage: registrycheck.NewCoverage(),
	}
	rapid.MakeFuzz(func(rt *rapid.T) { runner.exercise(rt, steps) })(t, input)
	runner.assertCoverage(t)
}

func (runner *nativeSequenceRunner) exercise(rt *rapid.T, steps int) {
	capabilities := runner.definition.Capabilities
	if capabilities.SessionEnd {
		runner.apply(rt, "session-a", nativeEventCase{event: "SessionEnd"}, nil, false)
	}
	if capabilities.SessionStart {
		runner.apply(rt, "session-a", nativeEventCase{event: "SessionStart", activity: registry.ActivityIdle}, nil, false)
		runner.apply(rt, "session-a", nativeEventCase{event: "SessionStart", activity: registry.ActivityRunning, source: "resume"}, nil, false)
	}
	if capabilities.WaitingPermission {
		runner.apply(rt, "session-a", nativeEventCase{event: "PostToolUse", activity: registry.ActivityRunning}, nil, false)
		runner.apply(rt, "session-a", nativeEventCase{event: "PermissionRequest", activity: registry.ActivityWaiting}, nil, false)
		runner.apply(rt, "session-a", nativeEventCase{event: "PermissionRequest", activity: registry.ActivityWaiting}, nil, false)
		runner.apply(rt, "session-a", nativeEventCase{event: "PostToolUse", activity: registry.ActivityRunning}, nil, false)
	}
	if !runner.definition.ExclusiveProcess {
		runner.apply(rt, "session-b", nativeEventCase{event: "SessionStart", activity: registry.ActivityIdle}, nil, false)
	}
	for range steps {
		sessionID := rapid.SampledFrom([]string{"session-a", "session-b"}).Draw(rt, "session")
		event := rapid.SampledFrom(lifecycleEvents(capabilities)).Draw(rt, "event")
		var payload json.RawMessage
		if rapid.Bool().Draw(rt, "optional-payload") {
			payload = json.RawMessage(`{"session_id":"payload-session","cwd":"/work"}`)
		}
		runner.apply(rt, sessionID, event, payload, rapid.IntRange(0, 5).Draw(rt, "replay") == 0)
	}
}

func (runner *nativeSequenceRunner) apply(rt *rapid.T, sessionID string, event nativeEventCase, payload json.RawMessage, stale bool) {
	rt.Helper()
	runner.tick++
	if !PayloadCompatibleWithHarness(runner.id, payload) {
		return
	}
	observation := runner.observation(rt, sessionID, event, payload, stale)
	assertEventLifecycle(rt, runner.definition.Capabilities, observation)
	session, before, accepted := runner.observeEvent(rt, sessionID, event, observation, stale)
	if accepted {
		runner.recordAccepted(rt, sessionID, event, observation, session, before)
	}
}

func (runner *nativeSequenceRunner) observeEvent(rt *rapid.T, sessionID string, event nativeEventCase, observation registry.Observation, stale bool) (registry.Session, []registry.Session, bool) {
	rt.Helper()
	before, err := runner.store.List(rt.Context(), registry.Filter{})
	if err != nil {
		rt.Fatal(err)
	}
	session, observeErr := runner.store.Observe(rt.Context(), observation)
	if observeErr != nil && !cleanObservationError(observeErr) {
		rt.Fatalf("untyped observation error for %q: %v", event.event, observeErr)
	}
	after, err := runner.store.List(rt.Context(), registry.Filter{})
	if err != nil {
		rt.Fatal(err)
	}
	if err := registrycheck.Sessions(after); err != nil {
		rt.Fatalf("registry invariant after %q: %v", event.event, err)
	}
	if stale && runner.sequenced && storedSequence(before, sessionID) > 0 && observeErr == nil {
		rt.Fatalf("stale sequence %d accepted for %q", storedSequence(before, sessionID), event.event)
	}
	if observeErr != nil {
		if !reflect.DeepEqual(before, after) {
			rt.Fatalf("rejected report changed registry state for %q", event.event)
		}
		return registry.Session{}, before, false
	}
	return session, before, true
}

func storedSequence(sessions []registry.Session, sessionID string) uint64 {
	for _, session := range sessions {
		if session.SessionID == sessionID && session.Observations.Native != nil && session.Observations.Native.Reporter.Sequence != nil {
			return *session.Observations.Native.Reporter.Sequence
		}
	}
	return 0
}

func cleanObservationError(err error) bool {
	return errors.Is(err, registry.ErrObservationConflict) || errors.Is(err, registry.ErrInvalidObservation)
}

func (runner *nativeSequenceRunner) observation(rt *rapid.T, sessionID string, event nativeEventCase, payload json.RawMessage, stale bool) registry.Observation {
	rt.Helper()
	defaults, err := DefaultsFromPayloadWithError(runner.id, payload)
	if err != nil {
		rt.Fatalf("payload defaults: %v", err)
	}
	attributes := defaults.Attributes
	if event.source != "" {
		attributes = map[string]string{"source": event.source}
	}
	result := registry.Observation{
		Harness: runner.id, At: runner.base.Add(time.Duration(runner.tick) * time.Millisecond),
		Subject: registry.ObservationIdentity{SessionID: sessionID, SessionPath: defaults.SessionPath},
		Evidence: &registry.Report{
			Reporter: registry.Reporter{Integration: runner.definition.IntegrationSource, Version: runner.definition.IntegrationVersion, MultiSession: !runner.definition.ExclusiveProcess},
			Event:    event.event, Attributes: attributes, Payload: payload,
		},
	}
	if event.activity != "" {
		result.Report().Activity = &event.activity
	}
	if runner.sequenced {
		value := runner.sequences[sessionID] + 1
		if stale && runner.sequences[sessionID] > 0 {
			value = runner.sequences[sessionID]
		}
		result.Report().Reporter.Sequence = &value
	}
	return PrepareObservation(result)
}

func assertEventLifecycle(rt *rapid.T, capabilities harness.Capabilities, observation registry.Observation) {
	rt.Helper()
	lifecycle := observation.Report().Lifecycle
	if lifecycle != nil && !lifecycle.IsValid() {
		rt.Fatalf("invalid translated lifecycle for %q", observation.Report().Event)
	}
	if !capabilities.SessionEnd {
		return
	}
	switch observation.Report().Event {
	case "SessionEnd", "session_end", "on_session_finalize":
		if lifecycle == nil || *lifecycle != registry.NativeLifecycleEnd {
			rt.Fatalf("end event %q did not translate to End", observation.Report().Event)
		}
	}
}

func (runner *nativeSequenceRunner) recordAccepted(rt *rapid.T, sessionID string, event nativeEventCase, observation registry.Observation, session registry.Session, before []registry.Session) {
	rt.Helper()
	if session.Observations.Native != nil && session.Observations.Native.Reporter.Sequence != nil {
		runner.sequences[sessionID] = *session.Observations.Native.Reporter.Sequence
	}
	runner.coverage.Hit("accepted")
	runner.coverage.Hit("session:" + sessionID)
	if lifecycle := observation.Report().Lifecycle; lifecycle != nil {
		runner.coverage.Hit(string(*lifecycle))
		if *lifecycle == registry.NativeLifecycleEnd && (session.Presence() != registry.PresenceGone || session.Activity() != nil) {
			rt.Fatalf("end left presence %s and activity %v", session.Presence(), session.Activity())
		}
	}
	runner.assertPermission(rt, event, session, before)
	assertNoRevival(rt, sessionID, event.event, observation.Report().Lifecycle, session, before)
}

func assertNoRevival(rt *rapid.T, sessionID, event string, lifecycle *registry.NativeLifecycle, session registry.Session, before []registry.Session) {
	rt.Helper()
	for _, prior := range before {
		if prior.SessionID != sessionID || prior.Presence() != registry.PresenceGone {
			continue
		}
		if lifecycle != nil && (*lifecycle == registry.NativeLifecycleStart || *lifecycle == registry.NativeLifecycleResume) {
			continue
		}
		if session.Presence() != registry.PresenceGone {
			rt.Fatalf("non-start event %q revived ended session", event)
		}
	}
}

func (runner *nativeSequenceRunner) assertPermission(rt *rapid.T, event nativeEventCase, session registry.Session, before []registry.Session) {
	rt.Helper()
	if event.activity == registry.ActivityWaiting {
		runner.assertWaitingEvidence(rt, event.event, session)
		return
	}
	if event.activity != registry.ActivityRunning && event.activity != registry.ActivityIdle {
		return
	}
	runner.assertResolvedEvidence(rt, event.event, session, before)
}

func (runner *nativeSequenceRunner) assertWaitingEvidence(rt *rapid.T, event string, session registry.Session) {
	rt.Helper()
	if !runner.definition.Capabilities.WaitingPermission {
		rt.Fatal("waiting activity emitted without adapter capability")
	}
	if session.Presence() == registry.PresenceGone {
		return
	}
	if session.Observations.Native == nil || session.Observations.Native.Activity == nil || *session.Observations.Native.Activity != registry.ActivityWaiting {
		rt.Fatalf("permission request %q did not record waiting evidence", event)
	}
	if runner.definition.StateAuthority == registry.AuthorityHook && (session.Activity() == nil || *session.Activity() != registry.ActivityWaiting) {
		rt.Fatalf("permission request %q did not enter waiting", event)
	}
	runner.coverage.Hit("permission")
}

func (runner *nativeSequenceRunner) assertResolvedEvidence(rt *rapid.T, event string, session registry.Session, before []registry.Session) {
	rt.Helper()
	for _, prior := range before {
		if !priorHadWaitingEvidence(prior, session) {
			continue
		}
		if session.Observations.Native == nil || session.Observations.Native.Activity == nil || *session.Observations.Native.Activity == registry.ActivityWaiting {
			rt.Fatalf("resolution event %q left native permission waiting", event)
		}
		if runner.definition.StateAuthority == registry.AuthorityHook && session.Activity() != nil && *session.Activity() == registry.ActivityWaiting {
			rt.Fatalf("resolution event %q left permission waiting", event)
		}
	}
}

func priorHadWaitingEvidence(prior, session registry.Session) bool {
	return prior.SessionID == session.SessionID && prior.Observations.Native != nil && prior.Observations.Native.Activity != nil && *prior.Observations.Native.Activity == registry.ActivityWaiting && session.Presence() != registry.PresenceGone
}

func (runner *nativeSequenceRunner) assertCoverage(t *testing.T) {
	t.Helper()
	capabilities := runner.definition.Capabilities
	required := []string{"accepted"}
	if capabilities.SessionStart {
		required = append(required, string(registry.NativeLifecycleStart), string(registry.NativeLifecycleResume))
	}
	if capabilities.SessionEnd {
		required = append(required, string(registry.NativeLifecycleEnd))
	}
	if capabilities.WaitingPermission {
		required = append(required, "permission")
	}
	if !runner.definition.ExclusiveProcess {
		required = append(required, "session:session-a", "session:session-b")
	}
	if missing := runner.coverage.Missing(required...); len(missing) > 0 {
		t.Fatalf("missing adapter event coverage: %v", missing)
	}
}

func lifecycleEvents(capabilities harness.Capabilities) []nativeEventCase {
	events := []nativeEventCase{
		{event: "UserPromptSubmit", activity: registry.ActivityRunning},
		{event: "PostToolUse", activity: registry.ActivityRunning},
		{event: "Stop", activity: registry.ActivityIdle},
		{event: "agent_start", activity: registry.ActivityRunning},
		{event: "agent_end", activity: registry.ActivityIdle},
	}
	if capabilities.SessionStart {
		events = append(events, nativeEventCase{event: "SessionStart", activity: registry.ActivityIdle}, nativeEventCase{event: "session_start", activity: registry.ActivityIdle}, nativeEventCase{event: "session_switch", activity: registry.ActivityRunning})
	}
	if capabilities.SessionEnd {
		events = append(events, nativeEventCase{event: "SessionEnd"}, nativeEventCase{event: "session_end"}, nativeEventCase{event: "on_session_finalize"})
	}
	if capabilities.WaitingPermission {
		events = append(events, nativeEventCase{event: "PermissionRequest", activity: registry.ActivityWaiting}, nativeEventCase{event: "tool_approval_requested", activity: registry.ActivityWaiting}, nativeEventCase{event: "PermissionDenied", activity: registry.ActivityIdle}, nativeEventCase{event: "tool_approval_resolved", activity: registry.ActivityRunning})
	}
	return events
}

func lifecycleSeed(name string, index uint64) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(name))
	return hash.Sum64() + index
}

func lifecycleBytes(seed uint64) []byte {
	data := make([]byte, 8192)
	var input [16]byte
	binary.LittleEndian.PutUint64(input[:8], seed)
	for offset := 0; offset < len(data); offset += sha256.Size {
		binary.LittleEndian.PutUint64(input[8:], uint64(offset/sha256.Size))
		digest := sha256.Sum256(input[:])
		copy(data[offset:], digest[:])
	}
	return data
}
