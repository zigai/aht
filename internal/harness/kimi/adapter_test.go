package kimi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"pgregory.net/rapid"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPayloadDefaultsPreservesSessionLookupFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	// A regular file where the sessions directory belongs produces a portable
	// filesystem error, including when the tests run with elevated permissions.
	if err := os.WriteFile(filepath.Join(home, "sessions"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New().PayloadDefaults(map[string]any{"session_id": "wanted"})
	if _, ok := errors.AsType[*os.PathError](err); !ok {
		t.Fatalf("error = %v, want session lookup path error", err)
	}
}

func TestKimiCodeSessionPathReturnsCurrentSessionDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)

	sessionPath := filepath.Join(home, "sessions", "work-hash", "wanted")
	if err := os.MkdirAll(sessionPath, 0o700); err != nil {
		t.Fatal(err)
	}

	path, err := kimiCodeSessionPath("wanted")
	if err != nil {
		t.Fatal(err)
	}

	if path != sessionPath {
		t.Fatalf("session path = %q, want %q", path, sessionPath)
	}
}

func TestKimiCodeSessionPathRejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)

	for _, directory := range []string{
		filepath.Join(
			"sessions",
			"work-hash",
			"nested",
		),
		filepath.Join(
			"sessions",
			"wanted",
		),
	} {
		if err := os.MkdirAll(filepath.Join(home, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	for _, sessionID := range []string{"../wanted", "..", "."} {
		path, err := kimiCodeSessionPath(sessionID)
		if err != nil {
			t.Fatal(err)
		}

		if path != "" {
			t.Fatalf("session path for %q = %q, want empty", sessionID, path)
		}
	}
}

func TestNativeHookPayloadUsesCLIIdentity(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name       string
		payload    string
		compatible bool
	}{
		{name: "CLI", payload: `{"session_id":"session-example","cwd":"/work",` +
			`"hook_event_name":"PermissionRequest",` +
			`"client_type":"kimi_code_cli"}`, compatible: true},
		{name: "missing client", payload: `{"session_id":"session-example","cwd":"/work",` +
			`"hook_event_name":"PermissionRequest"}`, compatible: false},
		{name: "desktop", payload: `{"session_id":"session-example","cwd":"/work",` +
			`"hook_event_name":"SessionStart",` +
			`"client_type":"kimi_code_desktop"}`, compatible: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := New().PayloadCompatible(json.RawMessage(scenario.payload)); got != scenario.compatible {
				t.Fatalf("payload compatibility = %t, want %t", got, scenario.compatible)
			}
		})
	}
}

func TestNativeLifecyclePreservesResumeAndArchive(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		event     string
		source    string
		lifecycle registry.NativeLifecycle
		presence  registry.Presence
	}{
		{
			event:     "SessionStart",
			source:    "startup",
			lifecycle: registry.NativeLifecycleStart,
			presence:  registry.PresenceLive,
		},
		{
			event:     "SessionStart",
			source:    "resume",
			lifecycle: registry.NativeLifecycleResume,
			presence:  registry.PresenceLive,
		},
		{
			event:     "SessionEnd",
			source:    "archive",
			lifecycle: registry.NativeLifecycleEnd,
			presence:  registry.PresenceGone,
		},
	} {
		t.Run(scenario.event+scenario.source, func(t *testing.T) {
			got := New().LifecycleDefaults(
				"",
				map[string]string{
					"kimi_code_hook_event_name": scenario.event,
					"kimi_code_source":          scenario.source,
				},
			)
			if got.Event != scenario.event || got.Lifecycle != scenario.lifecycle ||
				got.Presence != scenario.presence {
				t.Fatalf("native lifecycle = %+v", got)
			}
		})
	}
}

func TestNativePermissionDetailClearsAfterResolution(t *testing.T) {
	t.Parallel()

	adapter := New()

	waiting := adapter.ActivityDetail("PermissionRequest", registry.ActivityWaiting, nil)
	if waiting == nil || *waiting != registry.ActivityDetailPermission {
		t.Fatalf("permission detail = %v", waiting)
	}

	if got := adapter.ActivityDetail(
		"PermissionResult",
		registry.ActivityRunning,
		nil,
	); got != nil {
		t.Fatalf("resolved permission retained waiting detail: %v", got)
	}
}

func TestTOMLQuoteStringPreservesGeneratedStrings(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(property *rapid.T) {
		value := rapid.String().Draw(property, "value")

		var decoded map[string]string
		if err := toml.Unmarshal([]byte("value = "+tomlQuoteString(value)), &decoded); err != nil {
			property.Fatalf("TOML parser rejected %q: %v", value, err)
		}

		if decoded["value"] != value {
			property.Fatalf("TOML round trip = %q, want %q", decoded["value"], value)
		}
	})
}

func TestTOMLQuoteStringRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []string{
		`simple`,
		`with "quotes" and 'single quotes'`,
		`with \backslashes\ and /slashes/`,
		// test fixture validates UTF-8 multi-byte rune handling in TOML strings
		"with unicode: 🚀 and 世界 and ñ",
		"with bell: \a",
		"with vertical tab: \v",
		"with delete: \x7f",
		"with mixed: \"\a\v\x7f\\🚀",
	}
	for _, tc := range cases {
		quoted := tomlQuoteString(tc)

		var decoded map[string]string
		if err := toml.Unmarshal([]byte("value = "+quoted), &decoded); err != nil {
			t.Fatalf("TOML parser rejected %q: %v", quoted, err)
		}

		if decoded["value"] != tc {
			t.Errorf("roundtrip mismatch: got %q, want %q", decoded["value"], tc)
		}
	}
}

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KIMI_CODE_HOME", t.TempDir())

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
			name: "kimi",

			payload: `{"session_id":"session-no-index","cwd":"/tmp",` +
				`"hook_event_name":"PermissionRequest",` +
				`"client_type":"kimi_code_cli","tool_name":"Bash",` +
				`"turn_id":7}`,
			wantID:     "session-no-index",
			wantPath:   "",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "PermissionRequest",
			wantAttr:   "kimi_code_tool_name",
			wantAttrKV: "Bash",
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
			name: "kimi-code accepts native hook payload",

			payload: `{"session_id":"session-example","cwd":"/repo",` +
				`"hook_event_name":"PermissionRequest",` +
				`"client_type":"kimi_code_cli"}`,
			want: true,
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

func TestKimiPayloadDefaultsUsesCurrentSessionDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)

	sessionDir := filepath.Join(home, "sessions", "wd_repo_abc", "kimi-index-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("creating Kimi session directory: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(
		`{"session_id":"kimi-index-session","cwd":"/repo",`+
			`"hook_event_name":"SessionStart",`+
			`"client_type":"kimi_code_cli","source":"startup"}`,
	), &payload); err != nil {
		t.Fatal(err)
	}

	got, err := New().PayloadDefaults(payload)
	if err != nil {
		t.Fatalf("unexpected error from PayloadDefaults: %v", err)
	}

	if got.SessionID != "kimi-index-session" ||
		got.SessionPath != sessionDir ||
		got.CWD != "/repo" ||
		got.Event != "SessionStart" {
		t.Fatalf("unexpected defaults: %#v", got)
	}

	if got.Attributes["kimi_code_source"] != "startup" {
		t.Fatalf("expected kimi_code_source=startup, got %#v", got.Attributes)
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("kimi-code"), variable: "KIMI_CODE_HOME"},
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

func TestPayloadDefaultsOmitSessionPathWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		payload string
		plant   func(t *testing.T, workDir string)
	}{
		{
			harness: registry.Harness("kimi-code"),
			payload: `{"session_id":"abc","cwd":"/repo","hook_event_name":"SessionStart"}`,
			plant: func(t *testing.T, workDir string) {
				t.Helper()

				if err := os.MkdirAll(
					filepath.Join(
						workDir,
						".kimi-code",
						"sessions",
						"wd",
						"abc",
					),
					0o700,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			test.plant(t, testharness.WithoutHome(t, New()))

			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}

			got, err := New().PayloadDefaults(payload)
			if err != nil {
				t.Fatalf("PayloadDefaults: %v", err)
			}

			if got.SessionID != "abc" || got.SessionPath != "" {
				t.Fatalf(
					"PayloadDefaults without a home = %+v, want session %q and no path",
					got,
					"abc",
				)
			}
		})
	}
}
