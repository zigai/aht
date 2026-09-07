package install

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

const droidUserCommand = "/opt/local/bin/user-droid-hook"

func TestDroidRepairsWrappedHooksAndPreservesUserCommands(t *testing.T) {
	for _, test := range []struct {
		name        string
		withForeign bool
	}{{name: "managed-only"}, {name: "with-user-hook", withForeign: true}} {
		t.Run(test.name, func(t *testing.T) {
			options, path := writeWrappedDroidHooks(t, test.withForeign)
			status, err := Inspect(registry.HarnessDroid, testInstallBinary)
			if err != nil || status.Status != ArtifactStale {
				t.Fatalf("wrapped hooks status = %+v, error = %v", status, err)
			}
			repaired, err := Run(options)
			if err != nil || !repaired.Changed {
				t.Fatalf("repair = %+v, error = %v", repaired, err)
			}
			config := decodeTestJSONObject(t, []byte(repaired.Snippet), "repaired Droid hooks")
			requireTestHookEvents(t, config, []string{hookEventSessionStart, hookEventStop, "SessionEnd"})
			requireDroidWrappedUserHooks(t, config, test.withForeign)
			status, err = Inspect(registry.HarnessDroid, testInstallBinary)
			if err != nil || status.Status != ArtifactCurrent {
				t.Fatalf("repaired hooks status = %+v, error = %v", status, err)
			}
			if _, err := Remove(options); err != nil {
				t.Fatal(err)
			}
			remaining := readTestFile(t, path, "removed Droid hooks")
			if strings.Contains(string(remaining), "aht_integration=droid-hook") {
				t.Fatalf("removal left managed hooks: %s", remaining)
			}
			requireDroidWrappedUserHooks(t, decodeTestJSONObject(t, remaining, "removed Droid hooks"), test.withForeign)
		})
	}
}

func writeWrappedDroidHooks(t *testing.T, withForeign bool) (Options, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	options := Options{Harness: registry.HarnessDroid, Binary: testInstallBinary}
	installed, err := Run(options)
	if err != nil {
		t.Fatal(err)
	}
	hooks := decodeTestJSONObject(t, []byte(installed.Snippet), "Droid hooks")
	if withForeign {
		groups, ok := hooks["Stop"].([]any)
		if !ok {
			t.Fatal("missing Stop hooks")
		}
		hooks["Stop"] = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": droidUserCommand}}})
	}
	data, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return options, installed.Path
}

func requireDroidWrappedUserHooks(t *testing.T, config map[string]any, withForeign bool) {
	t.Helper()
	var want any
	if withForeign {
		want = map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": droidUserCommand}}}}}
	}
	if !reflect.DeepEqual(config["hooks"], want) {
		t.Fatalf("wrapped user hooks = %#v, want %#v", config["hooks"], want)
	}
}
