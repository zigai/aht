package cursor

import (
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

//go:embed testdata/install.sh
var nativeInstaller []byte

func TestDistributionExposesBinaryInSuppliedDirectory(t *testing.T) {
	home := testharness.Isolate(t, New())

	work, bin := t.TempDir(), filepath.Join(t.TempDir(), "custom bin")
	if err := os.WriteFile(
		filepath.Join(work, "install-cursor.sh"),
		nativeInstaller,
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	for _, step := range New().Distribution().Install("", work, bin) {
		if step.Name == "curl" {
			continue
		}

		command := exec.CommandContext(t.Context(), step.Name, step.Args...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("install command: %v: %s", err, output)
		}
	}

	command := exec.CommandContext(t.Context(), filepath.Join(bin, "cursor-agent"))

	output, err := command.Output()
	if err != nil || string(output) != "ready\n" {
		t.Fatalf("installed command under %s: %q, %v", home, output, err)
	}
}

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name string

		payload    string
		wantID     string
		wantPath   string
		wantCWD    string
		wantRoot   string
		wantEvent  string
		wantAttr   string
		wantAttrKV string
	}{
		{
			name: "cursor",

			payload: `{"conversation_id":"cursor-conversation",` +
				`"session_id":"cursor-session","transcript_path":"/tmp/` +
				`cursor.jsonl","workspace_roots":["/repo"],` +
				`"hook_event_name":"beforeSubmitPrompt","model":"gpt-5.2",` +
				`"cursor_version":"1.7.2","composer_mode":"agent",` +
				`"is_background_agent":false}`,
			wantID:     "cursor-session",
			wantPath:   "/tmp/cursor.jsonl",
			wantCWD:    "/repo",
			wantRoot:   "/repo",
			wantEvent:  "beforeSubmitPrompt",
			wantAttr:   "cursor_model",
			wantAttrKV: "gpt-5.2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}

			got, err := New().PayloadDefaults(payload)
			if err != nil {
				t.Fatalf("unexpected error from PayloadDefaults: %v", err)
			}

			if got.SessionID != test.wantID ||
				got.SessionPath != test.wantPath ||
				got.CWD != test.wantCWD ||
				got.ProjectRoot != test.wantRoot ||
				got.Event != test.wantEvent {
				t.Fatalf("unexpected defaults: %#v", got)
			}

			if got.Attributes[test.wantAttr] != test.wantAttrKV {
				t.Fatalf(
					"expected attribute %s=%q, got %#v",
					test.wantAttr,
					test.wantAttrKV,
					got.Attributes,
				)
			}
		})
	}
}

func TestPayloadCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string

		payload string
		want    bool
	}{
		{
			name: "cursor accepts native hook payload",

			payload: `{"conversation_id":"cursor-conversation",` +
				`"session_id":"cursor-session","transcript_path":null,` +
				`"workspace_roots":["/repo"],"hook_event_name":"sessionEnd",` +
				`"cursor_version":"2026.06.15"}`,
			want: true,
		},
		{
			name: "cursor rejects payload without cursor common fields",

			payload: `{"session_id":"cursor-session","hook_event_name":"sessionStart"}`,
			want:    false,
		},
		{
			name: "cursor rejects string workspace roots",

			payload: `{"session_id":"cursor-session","transcript_path":"/tmp/` +
				`cursor.jsonl","workspace_roots":"/repo",` +
				`"hook_event_name":"sessionEnd",` +
				`"cursor_version":"2026.06.15"}`,
			want: false,
		},
		{
			name: "cursor rejects blank workspace roots",

			payload: `{"session_id":"cursor-session","transcript_path":"/tmp/` +
				`cursor.jsonl","workspace_roots":["  "],` +
				`"hook_event_name":"sessionEnd",` +
				`"cursor_version":"2026.06.15"}`,
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := New().PayloadCompatible(json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("cursor"), variable: "CURSOR_CONFIG_DIR"},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, New())

				if !knownHome {
					t.Setenv("HOME", "")
					t.Setenv("USERPROFILE", "")
				}

				override := t.TempDir()

				value := override
				if test.value != nil {
					value = test.value(override)
				}

				t.Setenv(test.variable, value)

				locations := New().Locations("")

				config := testharness.LocationPaths(locations, harness.LocationKindConfig)
				if len(config) == 0 || slices.ContainsFunc(config, func(path string) bool {
					return !strings.HasPrefix(path, override+string(filepath.Separator)) &&
						!strings.HasPrefix(path, "/etc/")
				}) {
					t.Errorf("config locations with %s=%s = %v", test.variable, value, config)
				}
			})
		}
	}
}
