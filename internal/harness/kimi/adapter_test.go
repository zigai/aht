package kimi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	t.Setenv("KIMI_CODE_HOME", t.TempDir())

	path, err := kimiCodeSessionPath("../wanted")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("session path = %q, want empty", path)
	}
}

func TestNativeHookPayloadUsesCLIIdentity(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name       string
		payload    string
		compatible bool
	}{
		{name: "CLI", payload: `{"session_id":"session-example","cwd":"/work","hook_event_name":"PermissionRequest","client_type":"kimi_code_cli"}`, compatible: true},
		{name: "missing client", payload: `{"session_id":"session-example","cwd":"/work","hook_event_name":"PermissionRequest"}`, compatible: false},
		{name: "desktop", payload: `{"session_id":"session-example","cwd":"/work","hook_event_name":"SessionStart","client_type":"kimi_code_desktop"}`, compatible: false},
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
		{event: "SessionStart", source: "startup", lifecycle: registry.NativeLifecycleStart, presence: registry.PresenceLive},
		{event: "SessionStart", source: "resume", lifecycle: registry.NativeLifecycleResume, presence: registry.PresenceLive},
		{event: "SessionEnd", source: "archive", lifecycle: registry.NativeLifecycleEnd, presence: registry.PresenceGone},
	} {
		t.Run(scenario.event+scenario.source, func(t *testing.T) {
			got := New().LifecycleDefaults("", map[string]string{"kimi_code_hook_event_name": scenario.event, "kimi_code_source": scenario.source})
			if got.Event != scenario.event || got.Lifecycle != scenario.lifecycle || got.Presence != scenario.presence {
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
	if got := adapter.ActivityDetail("PermissionResult", registry.ActivityRunning, nil); got != nil {
		t.Fatalf("resolved permission retained waiting detail: %v", got)
	}
}

func TestTOMLQuoteStringRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []string{
		`simple`,
		`with "quotes" and 'single quotes'`,
		`with \backslashes\ and /slashes/`,
		"with unicode: 🚀 and 世界 and ñ", //nolint:gosmopolitan // test fixture validates UTF-8 multi-byte rune handling in TOML strings
		"with bell: \a",
		"with vertical tab: \v",
		"with delete: \x7f",
		"with mixed: \"\a\v\x7f\\🚀",
	}
	for _, tc := range cases {
		quoted := tomlQuoteString(tc)
		if strings.Contains(quoted, "\x7f") {
			t.Errorf("quoted string contains literal DEL: %q", quoted)
		}
		if strings.Contains(quoted, `\a`) || strings.Contains(quoted, `\v`) {
			t.Errorf("quoted string contains invalid TOML escape \\a or \\v: %q", quoted)
		}
		var unquoted string
		if err := json.Unmarshal([]byte(quoted), &unquoted); err != nil {
			t.Errorf("unmarshal failed for %q: %v", quoted, err)
		}
		if unquoted != tc {
			t.Errorf("roundtrip mismatch: got %q, want %q", unquoted, tc)
		}
	}
}
