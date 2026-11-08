package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/amp"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errTestCatalogCommand = errors.New("catalog command failed")

type testCatalogAdapter struct {
	harness.Adapter

	definition harness.Definition
	sessions   []harness.CatalogSession
	err        error
}

func (adapter testCatalogAdapter) Definition() harness.Definition {
	return adapter.definition
}

func (adapter testCatalogAdapter) Catalog(context.Context) ([]harness.CatalogSession, error) {
	return adapter.sessions, adapter.err
}

func newTestCatalogAdapter(
	id registry.Harness,
	source string,
	sessions []harness.CatalogSession,
	err error,
) testCatalogAdapter {
	return testCatalogAdapter{
		Adapter: amp.New(),
		definition: harness.Definition{
			ID: id, IntegrationSource: source, IntegrationVersion: 7,
		},
		sessions: sessions,
		err:      err,
	}
}

func TestNativeCatalogComposesPartialProviderResults(t *testing.T) {
	t.Parallel()

	providerFailure := errTestCatalogCommand

	entries, err := nativeCatalog(t.Context(), []harness.Adapter{
		newTestCatalogAdapter(
			"one",
			"one-catalog",
			[]harness.CatalogSession{
				{SessionID: "current", Current: true, Activity: registry.ActivityRunning},
			},
			nil,
		),
		newTestCatalogAdapter(
			"two",
			"two-catalog",
			[]harness.CatalogSession{{SessionID: "saved", Current: false}},
			providerFailure,
		),
		newTestCatalogAdapter("three", "three-catalog", nil, harness.ErrCatalogUnavailable),
	})
	if !errors.Is(err, providerFailure) {
		t.Fatalf("NativeCatalog() error = %v, want provider failure", err)
	}

	if len(entries) != 2 || entries[0].Harness != "one" || entries[1].Harness != "two" {
		t.Fatalf("NativeCatalog() entries = %#v", entries)
	}

	if entries[0].Reporter.Integration != "one-catalog" || entries[0].Reporter.Version != 7 ||
		!entries[0].Reporter.MultiSession {
		t.Fatalf("native reporter = %#v", entries[0].Reporter)
	}
}

func TestPayloadDefaultsRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	for _, adapter := range All() {
		if _, ok := adapter.(harness.PayloadAdapter); !ok {
			continue
		}

		id := adapter.Definition().ID
		if _, err := PayloadDefaults(id, json.RawMessage(`"not-an-object"`)); err == nil {
			t.Errorf("%s PayloadDefaults accepted a non-object payload", id)
		}
	}
}

func TestHandleHookUnsupportedHarness(t *testing.T) {
	t.Parallel()

	ids := []registry.Harness{"unknown-harness"}

	for _, adapter := range All() {
		if _, ok := adapter.(harness.HookAdapter); !ok {
			ids = append(ids, adapter.Definition().ID)
		}
	}

	for _, id := range ids {
		if _, ok, err := HandleHook(t.Context(), id, "Stop", nil, nil, nil); ok || err != nil {
			t.Errorf("%s has no managed hook adapter: supported=%v err=%v", id, ok, err)
		}
	}
}

func TestNativeDetailsOmitUnsupportedAdapters(t *testing.T) {
	t.Parallel()

	for _, adapter := range All() {
		if _, ok := adapter.(harness.ActivityDetailer); ok {
			continue
		}

		report := &registry.Report{
			Event:    "PermissionRequest",
			Activity: new(registry.ActivityWaiting),
		}

		got := PrepareObservation(
			registry.Observation{Harness: adapter.Definition().ID, Evidence: report},
		)
		if got.Report().Detail != nil {
			t.Errorf(
				"%s reports native detail without a detail reader: %v",
				adapter.Definition().ID,
				got.Report().Detail,
			)
		}
	}
}

func TestScreenManifestMatchesAuthority(t *testing.T) {
	t.Parallel()

	for _, adapter := range All() {
		definition := adapter.Definition()
		needsScreen := definition.StateAuthority == registry.AuthorityScreen ||
			definition.ScreenFallback

		if hasManifest := definition.ScreenManifest != ""; hasManifest != needsScreen {
			t.Errorf("%s: screen manifest present = %t, screen authority or fallback = %t",
				definition.ID, hasManifest, needsScreen)
		}
	}
}

func TestLocationsForUnknownHarness(t *testing.T) {
	t.Parallel()

	locations, ok := LocationsFor(registry.Harness("no-such-harness"), t.TempDir())
	if ok || locations != nil {
		t.Fatalf("LocationsFor(unknown) = %v, %t, want nil, false", locations, ok)
	}
}

func checkLocationScope(
	t *testing.T,
	id registry.Harness,
	location harness.Location,
	projectDir string,
) {
	t.Helper()

	if !filepath.IsAbs(location.Path) {
		t.Errorf("%s: relative path %q", id, location.Path)
	}

	inProject := strings.HasPrefix(location.Path, projectDir+string(filepath.Separator))
	switch location.Scope {
	case harness.LocationScopeProject:
		if !inProject {
			t.Errorf("%s: project path %q is outside %q", id, location.Path, projectDir)
		}
	case harness.LocationScopeGlobal:
		if inProject {
			t.Errorf("%s: global path %q is inside the project", id, location.Path)
		}
	}
}

func TestLocationsStayWithinTheirScope(t *testing.T) {
	for _, adapter := range All() {
		for _, key := range adapter.Definition().Env.Config {
			t.Setenv(key, filepath.Join(t.TempDir(), "parent"))
		}
	}

	home := testharness.Isolate(t, All()...)
	projectDir := t.TempDir()

	for _, adapter := range All() {
		id := adapter.Definition().ID

		sources, err := adapter.Transcript().Sources(home)
		if err != nil {
			t.Fatal(err)
		}

		for _, path := range sources {
			if !strings.HasPrefix(path, home+string(filepath.Separator)) {
				t.Errorf("%s: history source %q outside isolated home %q", id, path, home)
			}
		}

		locations, ok := LocationsFor(id, projectDir)
		if !ok || len(locations) == 0 {
			t.Errorf("%s: LocationsFor = %d locations, %t", id, len(locations), ok)
			continue
		}

		seen := make(map[harness.Location]bool, len(locations))
		for _, location := range locations {
			if seen[location] {
				t.Errorf("%s: duplicate location %+v", id, location)
			}

			seen[location] = true
			checkLocationScope(t, id, location, projectDir)
		}

		withoutProject, _ := LocationsFor(id, "")
		for _, location := range withoutProject {
			if location.Scope != harness.LocationScopeGlobal ||
				(!strings.HasPrefix(location.Path, home) && !strings.HasPrefix(location.Path, "/etc/")) {
				t.Errorf(
					"%s: %+v returned without a project directory or outside the isolated home %q",
					id,
					location,
					home,
				)
			}
		}
	}
}

func TestLocationsOmitGlobalPathsWithoutHome(t *testing.T) {
	projectDir := t.TempDir()
	testharness.Isolate(t, All()...)

	withHome := map[string]bool{}

	for _, adapter := range All() {
		locations, _ := LocationsFor(adapter.Definition().ID, projectDir)
		for _, location := range locations {
			withHome[location.Path] = true
		}
	}

	testharness.WithoutHome(t, All()...)

	for _, adapter := range All() {
		id := adapter.Definition().ID

		locations, _ := LocationsFor(id, projectDir)
		for _, location := range locations {
			if !filepath.IsAbs(location.Path) {
				t.Errorf("%s: %+v is not absolute without a home directory", id, location)
			}

			if location.Scope == harness.LocationScopeGlobal && !withHome[location.Path] {
				t.Errorf("%s: %+v resolved without a home directory or override", id, location)
			}
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	if len(All()) == 0 {
		t.Fatal("empty catalog")
	}

	for _, adapter := range All() {
		def := adapter.Definition()
		for _, name := range append([]string{string(def.ID)}, def.Aliases...) {
			for _, input := range []string{name, "  " + strings.ToUpper(name) + "  "} {
				got, err := Parse(input)
				if err != nil || got != def.ID {
					t.Errorf("Parse(%q) = %q, %v; want %q", input, got, err, def.ID)
				}
			}
		}
	}

	for _, input := range []string{"", "unknown-harness"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) accepted unknown harness", input)
		}
	}
}

func TestResumeCommandFor(t *testing.T) {
	t.Parallel()

	for _, adapter := range All() {
		def := adapter.Definition()
		t.Run(string(def.ID), func(t *testing.T) {
			if args := ResumeCommandFor(def.ID, "", ""); args != nil {
				t.Fatalf("resume without a native identity = %v, want nil", args)
			}

			for _, path := range []string{"", "/sessions/native.jsonl"} {
				args := ResumeCommandFor(def.ID, "native-session", path)
				if len(args) < 2 || !slices.Contains(def.ProcessNames, args[0]) {
					t.Fatalf("resume executable is not a native process: %v", args)
				}

				if !slices.Contains(args, "native-session") &&
					(path == "" || !slices.Contains(args, path)) {
					t.Fatalf("resume lost the native identity: %v", args)
				}
			}
		})
	}

	if args := ResumeCommandFor("unknown-harness", "native-session", ""); args != nil {
		t.Fatalf("unknown resume = %v", args)
	}
}

func TestFromCommand(t *testing.T) {
	t.Parallel()

	for _, adapter := range All() {
		def := adapter.Definition()
		for _, name := range def.ProcessNames {
			for _, command := range []string{name, filepath.Join(t.TempDir(), name)} {
				got, ok := FromCommand(command)
				if !ok || got != def.ID {
					t.Errorf("FromCommand(%q) = %q, %t; want %q", command, got, ok, def.ID)
				}
			}
		}
	}

	for _, command := range []string{"", "unrelated-program", "/opt/bin/unrelated-program"} {
		if got, ok := FromCommand(command); ok {
			t.Errorf("FromCommand(%q) = %q, want unknown", command, got)
		}
	}
}

func TestActivityUsesDeclaredAuthority(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	after := at.Add(registry.IntegrationActivityLease + time.Second)

	for _, adapter := range All() {
		def := adapter.Definition()
		t.Run(string(def.ID), func(t *testing.T) {
			process := registry.ProcessIdentity{
				PID:           123,
				StartIdentity: "boot:123",
				Executable:    def.ProcessNames[0],
			}
			observations := []registry.Observation{
				{
					Harness: def.ID,
					At:      at,
					Subject: registry.ObservationIdentity{SessionID: "native-session"},
					Evidence: &registry.Report{
						Reporter: registry.Reporter{Integration: def.IntegrationSource},
						Event:    "active",
						Claim:    new(registry.PresenceLive),
						Activity: new(registry.ActivityRunning),
						Process:  &process,
					},
				},
				{
					Harness: def.ID,
					At:      after,
					Subject: registry.ObservationIdentity{SessionID: "native-session"},
					Evidence: (*registry.Reading)(
						&registry.ScreenObservation{
							Activity:   registry.ActivityIdle,
							Authority:  registry.AuthorityScreen,
							Reason:     "manifest_rule",
							RuleID:     "input_prompt",
							Process:    process,
							ObservedAt: after,
						},
					),
				},
			}

			state, _, err := registry.NewReducer(Rules{}).
				Apply(registry.State{}, observations, after)
			if err != nil {
				t.Fatal(err)
			}

			if len(state.Sessions) != 1 {
				t.Fatalf("session count=%d, want 1", len(state.Sessions))
			}

			wantActivity, wantAuthority := registry.ActivityIdle, registry.AuthorityScreen
			if def.StateAuthority == registry.AuthorityHook &&
				(!def.ScreenFallback || def.RetainNativeActivity) {
				wantActivity, wantAuthority = registry.ActivityRunning, registry.AuthorityHook
			}

			for _, session := range state.Sessions {
				if session.SessionID != "native-session" || session.Activity() == nil ||
					*session.Activity() != wantActivity ||
					session.Decision() == nil ||
					session.Decision().Authority != wantAuthority {
					t.Fatalf(
						"session identity=%q, activity=%v, decision=%v; want native identity and %s from %s",
						session.SessionID,
						session.Activity(),
						session.Decision(),
						wantActivity,
						wantAuthority,
					)
				}
			}
		})
	}
}
