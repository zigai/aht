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

//nolint:gocognit,cyclop,errcheck,forcetypeassert // test validates notification group map structure
func TestDroidNotificationMatchersUpgradeTogether(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	options := Options{Harness: registry.HarnessDroid, Binary: testInstallBinary}
	installed, err := Run(options)
	if err != nil {
		t.Fatal(err)
	}
	config := decodeTestJSONObject(t, []byte(installed.Snippet), "Droid hooks")
	groups, ok := config["Notification"].([]any)
	if !ok || len(groups) == 0 {
		t.Fatal("missing Notification hooks")
	}
	group := groups[0].(map[string]any)
	ownedHook := group["hooks"].([]any)[0]
	userHook := map[string]any{"type": "command", "command": droidUserCommand, "timeout": float64(17)}
	foreignGroups := []any{
		map[string]any{"matcher": "permission_prompt", "description": "user mixed group", "hooks": []any{userHook}},
		map[string]any{"matcher": "idle_prompt", "hooks": []any{map[string]any{"type": "command", "command": droidUserCommand + " --idle"}}},
		map[string]any{"matcher": "user_empty", "hooks": []any{}},
	}
	config["Notification"] = []any{
		map[string]any{"matcher": "permission_prompt", "description": "user mixed group", "hooks": []any{ownedHook, userHook}},
		foreignGroups[1],
		foreignGroups[2],
		map[string]any{"matcher": "obsolete_notification", "hooks": []any{ownedHook}},
		group,
		group,
	}
	config["userSetting"] = map[string]any{"enabled": true}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Run(options)
	if err != nil || !upgraded.Changed {
		t.Fatalf("upgrade = %+v, error = %v", upgraded, err)
	}
	upgradedBytes := readTestFile(t, installed.Path, "upgraded Droid hooks")
	config = decodeTestJSONObject(t, upgradedBytes, "upgraded Droid hooks")
	groups, ok = config["Notification"].([]any)
	if !ok || len(groups) != len(foreignGroups)+2 {
		t.Fatalf("Notification groups = %#v", config["Notification"])
	}
	if !reflect.DeepEqual(groups[:len(foreignGroups)], foreignGroups) {
		t.Fatalf("foreign Notification groups changed: %#v", groups)
	}
	for index, want := range []struct {
		matcher  string
		activity string
	}{
		{matcher: "permission_prompt", activity: "waiting"},
		{matcher: "idle_prompt", activity: "idle"},
	} {
		group := groups[len(foreignGroups)+index].(map[string]any)
		hooks, ok := group["hooks"].([]any)
		if group["matcher"] != want.matcher || !ok || len(hooks) != 1 {
			t.Fatalf("managed Notification group = %#v, want matcher %q", group, want.matcher)
		}
		command, _ := hooks[0].(map[string]any)["command"].(string)
		if !strings.Contains(command, "--activity "+want.activity) || !strings.Contains(command, "aht_integration=droid-hook") {
			t.Fatalf("matcher %q command = %q", want.matcher, command)
		}
	}
	repeated, err := Run(options)
	if err != nil || repeated.Changed {
		t.Fatalf("repeat install = %+v, error = %v", repeated, err)
	}
	if got := readTestFile(t, installed.Path, "repeated Droid hooks"); string(got) != string(upgradedBytes) {
		t.Fatal("repeat install rewrote current hooks")
	}
	status, err := Inspect(registry.HarnessDroid, testInstallBinary)
	if err != nil || status.Status != ArtifactCurrent {
		t.Fatalf("installed status = %+v, error = %v", status, err)
	}
	removed, err := Remove(options)
	if err != nil || !removed.Changed {
		t.Fatalf("remove = %+v, error = %v", removed, err)
	}
	remaining := decodeTestJSONObject(t, readTestFile(t, installed.Path, "removed Droid hooks"), "removed Droid hooks")
	wantRemaining := map[string]any{
		"Notification": foreignGroups,
		"userSetting":  map[string]any{"enabled": true},
	}
	if !reflect.DeepEqual(remaining, wantRemaining) {
		t.Fatalf("remaining hooks = %#v, want %#v", remaining, wantRemaining)
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
