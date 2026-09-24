package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const testSessionID = "abc"

func TestReportHookCommandRendersTypedTransitionDimension(t *testing.T) {
	t.Parallel()

	activity := harness.ReportHookCommand("aht", registry.Harness("codex"), registry.ActivityRunning, "turn", "test")
	if !strings.Contains(activity, "--activity running") || strings.Contains(activity, "--presence") {
		t.Fatalf("activity command = %q", activity)
	}
	presence := harness.ReportHookCommand("aht", registry.Harness("codex"), registry.PresenceGone, "stop", "test")
	if !strings.Contains(presence, "--presence gone") || strings.Contains(presence, "--activity") {
		t.Fatalf("presence command = %q", presence)
	}
}

func TestReportHookCommandRejectsInvalidTypedTransition(t *testing.T) {
	t.Parallel()

	deferredPanic := false
	func() {
		defer func() {
			deferredPanic = recover() != nil
		}()
		_ = harness.ReportHookCommand("aht", registry.Harness("codex"), registry.Activity("bogus"), "turn", "test")
	}()
	if !deferredPanic {
		t.Fatal("ReportHookCommand accepted an invalid activity")
	}
}

func TestPiIntegrationAdvertisesPromptWaiting(t *testing.T) {
	t.Parallel()

	adapter, ok := Find(registry.Harness("pi"))
	if !ok {
		t.Fatal("Pi adapter not found")
	}
	if !adapter.Definition().Capabilities.WaitingPermission {
		t.Fatal("Pi adapter does not advertise user prompt waiting")
	}
}

func TestResumeCommandFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		harness     registry.Harness
		sessionID   string
		sessionPath string
		want        []string
	}{
		{
			name:        "claude",
			harness:     registry.Harness("claude"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"claude", "--resume", testSessionID},
		},
		{
			name:        "codex",
			harness:     registry.Harness("codex"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"codex", "resume", testSessionID},
		},
		{
			name:        "cursor",
			harness:     registry.Harness("cursor"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"cursor-agent", "--resume", testSessionID},
		},
		{
			name:        "copilot",
			harness:     registry.Harness("copilot"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"copilot", "--resume", testSessionID},
		},
		{
			name:        "cline",
			harness:     registry.Harness("cline"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"cline", "--id", testSessionID},
		},
		{
			name:        "kimi",
			harness:     registry.Harness("kimi-code"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"kimi", "--session", testSessionID},
		},
		{
			name:        "grok",
			harness:     registry.Harness("grok"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"grok", "--resume", testSessionID},
		},
		{
			name:        "goose",
			harness:     registry.Harness("goose"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"goose", "session", "--resume", "--session-id", testSessionID},
		},
		{
			name:        "pi path",
			harness:     registry.Harness("pi"),
			sessionID:   testSessionID,
			sessionPath: "/tmp/session.jsonl",
			want:        []string{"pi", "--session", "/tmp/session.jsonl"},
		},
		{
			name:        "omp path",
			harness:     registry.Harness("omp"),
			sessionID:   testSessionID,
			sessionPath: "/tmp/omp-session.jsonl",
			want:        []string{"omp", "--session", "/tmp/omp-session.jsonl"},
		},
		{
			name:        "omp id",
			harness:     registry.Harness("omp"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"omp", "--session", testSessionID},
		},
		{
			name:        "opencode",
			harness:     registry.Harness("opencode"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"opencode", "--session", testSessionID},
		},
		{
			name:        "agy",
			harness:     registry.Harness("agy"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"agy", "--conversation", testSessionID},
		},
		{
			name:        "kilo",
			harness:     registry.Harness("kilo"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"kilo", "--session", testSessionID},
		},
		{
			name:        "droid",
			harness:     registry.Harness("droid"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"droid", "--resume", testSessionID},
		},
		{
			name:        "openclaw",
			harness:     registry.Harness("openclaw"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"openclaw", "tui", "--session", testSessionID},
		},
		{
			name:        "amp",
			harness:     registry.Harness("amp"),
			sessionID:   testSessionID,
			sessionPath: "",
			want:        []string{"amp", "threads", "continue", testSessionID},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := ResumeCommandFor(test.harness, test.sessionID, test.sessionPath)
			if !slices.Equal(got, test.want) {
				t.Fatalf("expected %#v, got %#v", test.want, got)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  registry.Harness
	}{
		{name: "codex", value: "codex", want: registry.Harness("codex")},
		{name: "cursor alias binary", value: "cursor-agent", want: registry.Harness("cursor")},
		{name: "cursor alias cli hyphen", value: "cursor-cli", want: registry.Harness("cursor")},
		{name: "cursor alias cli underscore", value: "cursor_cli", want: registry.Harness("cursor")},
		{name: "copilot", value: "copilot", want: registry.Harness("copilot")},
		{name: "copilot alias hyphen", value: "github-copilot-cli", want: registry.Harness("copilot")},
		{name: "copilot alias underscore", value: "github_copilot", want: registry.Harness("copilot")},
		{name: "cline", value: "cline", want: registry.Harness("cline")},
		{name: "claude alias hyphen", value: "claude-code", want: registry.Harness("claude")},
		{name: "claude alias underscore", value: "claude_code", want: registry.Harness("claude")},
		{name: "kimi-code", value: "kimi-code", want: registry.Harness("kimi-code")},
		{name: "omp", value: "omp", want: registry.Harness("omp")},
		{name: "ohmypi alias", value: "ohmypi", want: registry.Harness("omp")},
		{name: "oh-my-pi alias", value: "oh-my-pi", want: registry.Harness("omp")},
		{name: "kimi alias", value: "kimi", want: registry.Harness("kimi-code")},
		{name: "kimi alias underscore", value: "kimi_code", want: registry.Harness("kimi-code")},
		{name: "kimi alias compact", value: "kimicode", want: registry.Harness("kimi-code")},
		{name: "grok alias hyphen", value: "grok-build", want: registry.Harness("grok")},
		{name: "grok alias underscore", value: "grok_build", want: registry.Harness("grok")},
		{name: "goose", value: "goose", want: registry.Harness("goose")},
		{name: "opencode alias hyphen", value: "open-code", want: registry.Harness("opencode")},
		{name: "opencode alias underscore", value: "open_code", want: registry.Harness("opencode")},
		{name: "agy alias", value: "antigravity-cli", want: registry.Harness("agy")},
		{name: "agy google alias", value: "google_antigravity", want: registry.Harness("agy")},
		{name: "kilo", value: "kilo", want: registry.Harness("kilo")},
		{name: "kilo alias command", value: "kilocode", want: registry.Harness("kilo")},
		{name: "kilo alias hyphen", value: "kilo-code", want: registry.Harness("kilo")},
		{name: "kilo alias underscore", value: "kilo_code", want: registry.Harness("kilo")},
		{name: "droid", value: "droid", want: registry.Harness("droid")},
		{name: "droid factory alias", value: "factory", want: registry.Harness("droid")},
		{name: "droid factory cli alias", value: "factory_cli", want: registry.Harness("droid")},
		{name: "openclaw", value: "openclaw", want: registry.Harness("openclaw")},
		{name: "hermes", value: "hermes", want: registry.Harness("hermes")},
		{name: "hermes agent alias", value: "hermes-agent", want: registry.Harness("hermes")},
		{name: "amp", value: "amp", want: registry.Harness("amp")},
		{name: "amp alias code", value: "ampcode", want: registry.Harness("amp")},
		{name: "amp alias hyphen", value: "amp-code", want: registry.Harness("amp")},
		{name: "amp alias underscore", value: "amp_code", want: registry.Harness("amp")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := Normalize(test.value)
			if err != nil {
				t.Fatalf("Normalize returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}

func TestSupportedNames(t *testing.T) {
	t.Parallel()

	want := []string{
		"claude",
		"codex",
		"cursor",
		"copilot",
		"cline",
		"kimi-code",
		"grok",
		"goose",
		"pi",
		"omp",
		"opencode",
		"agy",
		"kilo",
		"droid",
		"openclaw",
		"hermes",
		"amp",
	}
	got := SupportedNames()
	if !slices.Equal(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestEnvNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field harness.EnvField
		want  []string
	}{
		{
			name:  "session id",
			field: harness.EnvSessionID,
			want: []string{
				"AHT_SESSION_ID",
				"AGENT_SESSION_ID",
				"CLAUDE_SESSION_ID",
				"CODEX_SESSION_ID",
				"GROK_SESSION_ID",
				"PI_SESSION_ID",
				"OPENCODE_SESSION_ID",
				"KILO_SESSION_ID",
				"AMP_THREAD_ID",
				"AMP_SESSION_ID",
			},
		},
		{
			name:  "event",
			field: harness.EnvEvent,
			want: []string{
				"AHT_EVENT",
				"AGENT_EVENT",
				"GROK_HOOK_EVENT",
				"KILO_EVENT",
			},
		},
		{
			name:  "session path",
			field: harness.EnvSessionPath,
			want: []string{
				"AHT_SESSION_PATH",
				"AGENT_SESSION_PATH",
				"CLAUDE_SESSION_PATH",
				"CODEX_SESSION_PATH",
				"CURSOR_TRANSCRIPT_PATH",
				"PI_SESSION_PATH",
				"OPENCODE_SESSION_PATH",
				"KILO_SESSION_PATH",
			},
		},
		{
			name:  "project root",
			field: harness.EnvProjectRoot,
			want: []string{
				"AHT_PROJECT_ROOT",
				"PROJECT_ROOT",
				"CURSOR_PROJECT_DIR",
				"CLAUDE_PROJECT_DIR",
				"GROK_WORKSPACE_ROOT",
				"KILO_PROJECT_ROOT",
				"FACTORY_PROJECT_DIR",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := EnvNames(test.field)
			if !slices.Equal(got, test.want) {
				t.Fatalf("expected %#v, got %#v", test.want, got)
			}
		})
	}
}

func TestFromCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    registry.Harness
		wantOK  bool
	}{
		{command: "omp", want: registry.Harness("omp"), wantOK: true},
		{command: "oh-my-pi", want: registry.Harness("omp"), wantOK: true},
		{command: "/usr/bin/codex", want: registry.Harness("codex"), wantOK: true},
		{command: "/usr/local/bin/cursor-agent", want: registry.Harness("cursor"), wantOK: true},
		{command: "/opt/bin/copilot", want: registry.Harness("copilot"), wantOK: true},
		{command: "cline", want: registry.Harness("cline"), wantOK: true},
		{command: "agent", want: "", wantOK: false},
		{command: "claude", want: registry.Harness("claude"), wantOK: true},
		{command: "kimi", want: registry.Harness("kimi-code"), wantOK: true},
		{command: "Kimi Code", want: registry.Harness("kimi-code"), wantOK: true},
		{command: "grok", want: registry.Harness("grok"), wantOK: true},
		{command: "grok-build", want: registry.Harness("grok"), wantOK: true},
		{command: "goose", want: registry.Harness("goose"), wantOK: true},
		{command: "pi", want: registry.Harness("pi"), wantOK: true},
		{command: "opencode", want: registry.Harness("opencode"), wantOK: true},
		{command: "agy", want: registry.Harness("agy"), wantOK: true},
		{command: "kilo", want: registry.Harness("kilo"), wantOK: true},
		{command: "kilocode", want: registry.Harness("kilo"), wantOK: true},
		{command: "kilo-code", want: registry.Harness("kilo"), wantOK: true},
		{command: "kilo_code", want: registry.Harness("kilo"), wantOK: true},
		{command: "droid", want: registry.Harness("droid"), wantOK: true},
		{command: "openclaw", want: registry.Harness("openclaw"), wantOK: true},
		{command: "hermes", want: registry.Harness("hermes"), wantOK: true},
		{command: "hermes-agent", want: registry.Harness("hermes"), wantOK: true},
		{command: "amp", want: registry.Harness("amp"), wantOK: true},
		{command: "zsh", want: "", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()

			got, ok := FromCommand(test.command)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("expected (%q, %t), got (%q, %t)", test.want, test.wantOK, got, ok)
			}
		})
	}
}

func TestDefaultsFromPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		harness    registry.Harness
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
			name:       "claude",
			harness:    registry.Harness("claude"),
			payload:    `{"session_id":"claude-session","transcript_path":"/tmp/claude.jsonl","cwd":"/tmp","hook_event_name":"SessionStart","source":"startup","model":"claude-sonnet-4-6"}`,
			wantID:     "claude-session",
			wantPath:   "/tmp/claude.jsonl",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "SessionStart",
			wantAttr:   "claude_model",
			wantAttrKV: "claude-sonnet-4-6",
		},
		{
			name:       "codex",
			harness:    registry.Harness("codex"),
			payload:    `{"session_id":"codex-session","transcript_path":"/tmp/codex.jsonl","cwd":"/tmp","hook_event_name":"UserPromptSubmit","model":"gpt-5-codex"}`,
			wantID:     "codex-session",
			wantPath:   "/tmp/codex.jsonl",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "UserPromptSubmit",
			wantAttr:   "codex_model",
			wantAttrKV: "gpt-5-codex",
		},
		{
			name:       "grok",
			harness:    registry.Harness("grok"),
			payload:    `{"sessionId":"grok-session","cwd":"/tmp","workspaceRoot":"/repo","hookEventName":"UserPromptSubmit","toolName":"run_terminal_command"}`,
			wantID:     "grok-session",
			wantPath:   "",
			wantCWD:    "/tmp",
			wantRoot:   "/repo",
			wantEvent:  "UserPromptSubmit",
			wantAttr:   "grok_tool_name",
			wantAttrKV: "run_terminal_command",
		},
		{
			name:       "goose",
			harness:    registry.Harness("goose"),
			payload:    `{"event":"PreToolUse","session_id":"goose-session","working_dir":"/repo/goose","tool_name":"shell"}`,
			wantID:     "goose-session",
			wantPath:   "",
			wantCWD:    "/repo/goose",
			wantRoot:   "/repo/goose",
			wantEvent:  "PreToolUse",
			wantAttr:   "goose_tool_name",
			wantAttrKV: "shell",
		},
		{
			name:       "cursor",
			harness:    registry.Harness("cursor"),
			payload:    `{"conversation_id":"cursor-conversation","session_id":"cursor-session","transcript_path":"/tmp/cursor.jsonl","workspace_roots":["/repo"],"hook_event_name":"beforeSubmitPrompt","model":"gpt-5.2","cursor_version":"1.7.2","composer_mode":"agent","is_background_agent":false}`,
			wantID:     "cursor-session",
			wantPath:   "/tmp/cursor.jsonl",
			wantCWD:    "/repo",
			wantRoot:   "/repo",
			wantEvent:  "beforeSubmitPrompt",
			wantAttr:   "cursor_model",
			wantAttrKV: "gpt-5.2",
		},
		{
			name:       "copilot",
			harness:    registry.Harness("copilot"),
			payload:    `{"sessionId":"copilot-session","timestamp":"2026-06-29T10:00:00Z","cwd":"/repo/copilot","toolName":"Bash"}`,
			wantID:     "copilot-session",
			wantPath:   "",
			wantCWD:    "/repo/copilot",
			wantRoot:   "",
			wantEvent:  "",
			wantAttr:   "copilot_tool_name",
			wantAttrKV: "Bash",
		},
		{
			name:       "cline",
			harness:    registry.Harness("cline"),
			payload:    `{"clineVersion":"3.2.1","hookName":"PreToolUse","taskId":"cline-task","sessionContext":{"rootSessionId":"cline-root"},"workspaceRoots":["/repo/cline"],"tool_call":{"name":"execute_command"}}`,
			wantID:     "cline-root",
			wantPath:   filepath.Join(os.Getenv("HOME"), ".cline", "data", "sessions", "cline-root", "cline-root.messages.json"),
			wantCWD:    "/repo/cline",
			wantRoot:   "/repo/cline",
			wantEvent:  "PreToolUse",
			wantAttr:   "cline_tool_name",
			wantAttrKV: "execute_command",
		},
		{
			name:       "kimi",
			harness:    registry.Harness("kimi-code"),
			payload:    `{"session_id":"kimi-payload-session-no-index","cwd":"/tmp","hook_event_name":"PermissionRequest","tool_name":"Bash","turn_id":7}`,
			wantID:     "kimi-payload-session-no-index",
			wantPath:   "",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "PermissionRequest",
			wantAttr:   "kimi_code_tool_name",
			wantAttrKV: "Bash",
		},
		{
			name:       "agy",
			harness:    registry.Harness("agy"),
			payload:    `{"conversationId":"agy-session","transcriptPath":"/repo/.gemini/antigravity/transcript.jsonl","workspacePaths":["/repo"],"event":"PreToolUse","toolCall":{"name":"run_command","args":{"Cwd":"/repo/subdir"}}}`,
			wantID:     "agy-session",
			wantPath:   "/repo/.gemini/antigravity/transcript.jsonl",
			wantCWD:    "/repo/subdir",
			wantRoot:   "/repo",
			wantEvent:  "PreToolUse",
			wantAttr:   "agy_tool_name",
			wantAttrKV: "run_command",
		},
		{
			name:       "droid",
			harness:    registry.Harness("droid"),
			payload:    `{"session_id":"droid-session","transcript_path":"/tmp/droid.jsonl","cwd":"/repo/droid","hook_event_name":"PreToolUse","tool_name":"Bash"}`,
			wantID:     "droid-session",
			wantPath:   "/tmp/droid.jsonl",
			wantCWD:    "/repo/droid",
			wantRoot:   "",
			wantEvent:  "PreToolUse",
			wantAttr:   "droid_tool_name",
			wantAttrKV: "Bash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := DefaultsFromPayloadWithError(test.harness, json.RawMessage(test.payload))
			if err != nil {
				t.Fatalf("unexpected error from DefaultsFromPayloadWithError: %v", err)
			}
			if got.SessionID != test.wantID ||
				got.SessionPath != test.wantPath ||
				got.CWD != test.wantCWD ||
				got.ProjectRoot != test.wantRoot ||
				got.Event != test.wantEvent {
				t.Fatalf("unexpected defaults: %#v", got)
			}
			if got.Attributes[test.wantAttr] != test.wantAttrKV {
				t.Fatalf("expected attribute %s=%q, got %#v", test.wantAttr, test.wantAttrKV, got.Attributes)
			}
		})
	}
}

func TestDefaultsFromPayloadWithErrorRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	if _, err := DefaultsFromPayloadWithError(registry.Harness("codex"), json.RawMessage(`"not-an-object"`)); err == nil {
		t.Fatal("DefaultsFromPayloadWithError accepted a non-object payload")
	}
}

func TestKimiDefaultsFromPayloadUsesCurrentSessionDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", home)

	sessionDir := filepath.Join(home, "sessions", "wd_repo_abc", "kimi-index-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("creating Kimi session directory: %v", err)
	}

	got, err := DefaultsFromPayloadWithError(
		registry.Harness("kimi-code"),
		json.RawMessage(`{"session_id":"kimi-index-session","cwd":"/repo","hook_event_name":"SessionStart","source":"startup"}`),
	)
	if err != nil {
		t.Fatalf("unexpected error from DefaultsFromPayloadWithError: %v", err)
	}

	if got.SessionID != "kimi-index-session" ||
		got.SessionPath != sessionDir ||
		got.CWD != "/repo" ||
		got.Event != "SessionStart" {
		t.Fatalf("unexpected defaults: %#v", got)
	}
	if got.Attributes["kimi_code_start_source"] != "startup" {
		t.Fatalf("expected kimi_code_start_source=startup, got %#v", got.Attributes)
	}
}

func TestPayloadCompatibleWithHarness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		harness registry.Harness
		payload string
		want    bool
	}{
		{
			name:    "claude accepts native hook payload",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"claude-session","transcript_path":"/home/zigai/.claude/projects/-repo/claude-session.jsonl","cwd":"/repo","hook_event_name":"Stop"}`,
			want:    true,
		},
		{
			name:    "codex accepts native hook payload",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":"/home/zigai/.codex/sessions/2026/06/18/rollout.jsonl","cwd":"/repo","hook_event_name":"Stop","model":"gpt-5-codex"}`,
			want:    true,
		},
		{
			name:    "codex accepts null transcript path",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":null,"cwd":"/repo","hook_event_name":"Stop","model":"gpt-5-codex"}`,
			want:    true,
		},
		{
			name:    "codex accepts session end payload without model",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":null,"cwd":"/repo","hook_event_name":"SessionEnd","reason":"other"}`,
			want:    true,
		},
		{
			name:    "cursor accepts native hook payload",
			harness: registry.Harness("cursor"),
			payload: `{"conversation_id":"cursor-conversation","session_id":"cursor-session","transcript_path":null,"workspace_roots":["/repo"],"hook_event_name":"sessionEnd","cursor_version":"2026.06.15"}`,
			want:    true,
		},
		{
			name:    "copilot accepts native hook payload",
			harness: registry.Harness("copilot"),
			payload: `{"sessionId":"copilot-session","timestamp":"2026-06-29T10:00:00Z","cwd":"/repo","toolName":"Bash"}`,
			want:    true,
		},
		{
			name:    "cline accepts native hook payload",
			harness: registry.Harness("cline"),
			payload: `{"hookName":"TaskStart","taskId":"cline-task","sessionContext":{"rootSessionId":"cline-root"},"workspaceRoots":["/repo"]}`,
			want:    true,
		},
		{
			name:    "grok accepts native hook payload",
			harness: registry.Harness("grok"),
			payload: `{"hookEventName":"stop","sessionId":"grok-session","cwd":"/repo","workspaceRoot":"/repo"}`,
			want:    true,
		},
		{
			name:    "grok accepts snake case hook payload",
			harness: registry.Harness("grok"),
			payload: `{"hook_event_name":"stop","session_id":"grok-session","cwd":"/repo","workspace_root":"/repo"}`,
			want:    true,
		},
		{
			name:    "goose accepts native hook payload",
			harness: registry.Harness("goose"),
			payload: `{"event":"Stop","session_id":"goose-session","working_dir":"/repo"}`,
			want:    true,
		},
		{
			name:    "kimi-code accepts native hook payload",
			harness: registry.Harness("kimi-code"),
			payload: `{"session_id":"kimi-session","cwd":"/repo","hook_event_name":"PermissionRequest"}`,
			want:    true,
		},
		{
			name:    "agy accepts native hook payload",
			harness: registry.Harness("agy"),
			payload: `{"conversationId":"agy-session","workspacePaths":["/repo"],"event":"Stop"}`,
			want:    true,
		},
		{
			name:    "agy accepts snake case hook payload",
			harness: registry.Harness("agy"),
			payload: `{"conversation_id":"agy-session","workspace_paths":["/repo"],"event":"Stop"}`,
			want:    true,
		},
		{
			name:    "droid accepts native hook payload",
			harness: registry.Harness("droid"),
			payload: `{"session_id":"droid-session","transcript_path":"/tmp/droid.jsonl","cwd":"/repo","hook_event_name":"Stop"}`,
			want:    true,
		},
		{
			name:    "claude rejects camel case hook payload",
			harness: registry.Harness("claude"),
			payload: `{"hookEventName":"stop","sessionId":"not-claude","cwd":"/repo","workspaceRoot":"/repo","promptId":"prompt"}`,
			want:    false,
		},
		{
			name:    "claude accepts cursor-compatible common payload",
			harness: registry.Harness("claude"),
			payload: `{"conversation_id":"cursor-conversation","session_id":"cursor-session","transcript_path":null,"cwd":"/repo","hook_event_name":"sessionEnd","cursor_version":"2026.06.15","workspace_roots":["/repo"]}`,
			want:    true,
		},
		{
			name:    "claude accepts configurable transcript path",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"codex-session","transcript_path":"/home/zigai/.codex/sessions/2026/06/18/rollout.jsonl","cwd":"/repo","hook_event_name":"Stop","model":"gpt-5-codex"}`,
			want:    true,
		},
		{
			name:    "claude accepts null transcript path",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"claude-session","transcript_path":null,"cwd":"/repo","hook_event_name":"Stop"}`,
			want:    true,
		},
		{
			name:    "codex accepts configurable transcript path",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"claude-session","transcript_path":"/home/zigai/.claude/projects/-repo/claude-session.jsonl","cwd":"/repo","hook_event_name":"SessionStart","model":"claude-sonnet-4-6"}`,
			want:    true,
		},
		{
			name:    "cursor rejects payload without cursor common fields",
			harness: registry.Harness("cursor"),
			payload: `{"session_id":"cursor-session","hook_event_name":"sessionStart"}`,
			want:    false,
		},
		{
			name:    "cursor rejects string workspace roots",
			harness: registry.Harness("cursor"),
			payload: `{"session_id":"cursor-session","transcript_path":"/tmp/cursor.jsonl","workspace_roots":"/repo","hook_event_name":"sessionEnd","cursor_version":"2026.06.15"}`,
			want:    false,
		},
		{
			name:    "cursor rejects blank workspace roots",
			harness: registry.Harness("cursor"),
			payload: `{"session_id":"cursor-session","transcript_path":"/tmp/cursor.jsonl","workspace_roots":["  "],"hook_event_name":"sessionEnd","cursor_version":"2026.06.15"}`,
			want:    false,
		},
		{
			name:    "copilot rejects payload without cwd",
			harness: registry.Harness("copilot"),
			payload: `{"sessionId":"copilot-session"}`,
			want:    false,
		},
		{
			name:    "cline rejects payload without hook name",
			harness: registry.Harness("cline"),
			payload: `{"taskId":"cline-task","sessionContext":{"rootSessionId":"cline-root"}}`,
			want:    false,
		},
		{
			name:    "goose rejects payload without session id",
			harness: registry.Harness("goose"),
			payload: `{"event":"Stop","working_dir":"/repo"}`,
			want:    false,
		},
		{
			name:    "droid rejects payload without event",
			harness: registry.Harness("droid"),
			payload: `{"session_id":"droid-session","cwd":"/repo"}`,
			want:    false,
		},
		{
			name:    "claude rejects non-object json",
			harness: registry.Harness("claude"),
			payload: `"not an object"`,
			want:    false,
		},
		{
			name:    "claude rejects invalid json",
			harness: registry.Harness("claude"),
			payload: `{"session_id":`,
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := PayloadCompatibleWithHarness(test.harness, json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

type agyHookTestCase struct {
	name         string
	event        string
	payload      map[string]any
	parentArgs   []string
	wantReport   bool
	wantActivity registry.Activity
	wantResponse map[string]any
}

func TestHandleHookAgy(t *testing.T) {
	t.Parallel()

	tests := []agyHookTestCase{
		{
			name:  "pre invocation reports running",
			event: "PreInvocation",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"invocationNum":  float64(3),
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityRunning,
			wantResponse: map[string]any{},
		},
		{
			name:  "pre tool use permission reports waiting",
			event: "PreToolUse",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"toolCall": map[string]any{
					"name": "ask_permission",
					"args": map[string]any{"Cwd": "/repo/pkg"},
				},
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityWaiting,
			wantResponse: map[string]any{"decision": "allow"},
		},
		{
			name:  "fully idle stop reports idle",
			event: "Stop",
			payload: map[string]any{
				"conversationId":    "agy-session",
				"transcriptPath":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths":    []any{"/repo"},
				"terminationReason": "model_stop",
				"fullyIdle":         true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityIdle,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop remains idle regardless of parent args",
			event: "Stop",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"fullyIdle":      true,
			},
			parentArgs:   []string{"agy", "--print", "hello"},
			wantReport:   true,
			wantActivity: registry.ActivityIdle,
			wantResponse: map[string]any{},
		},
		{
			name:  "empty post tool use does not report",
			event: "PostToolUse",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"toolCall":       nil,
			},
			parentArgs:   nil,
			wantReport:   false,
			wantActivity: "",
			wantResponse: map[string]any{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assertAgyHookResult(t, test)
		})
	}
}

func assertAgyHookResult(t *testing.T, test agyHookTestCase) {
	t.Helper()

	result, ok := HandleHook(
		registry.Harness("agy"),
		test.event,
		json.RawMessage(`{"test":true}`),
		test.payload,
		test.parentArgs,
	)
	if !ok {
		t.Fatal("expected agy hook adapter")
	}
	if !reflect.DeepEqual(result.Response, test.wantResponse) {
		t.Fatalf("expected response %#v, got %#v", test.wantResponse, result.Response)
	}
	if result.ReportOK != test.wantReport {
		t.Fatalf("expected report ok %v, got %v", test.wantReport, result.ReportOK)
	}
	if result.ReportOK {
		if result.Report.ActivityClaim() == nil {
			t.Fatal("expected activity")
		}
		if *result.Report.ActivityClaim() != test.wantActivity {
			t.Fatalf("expected activity %q, got %q", test.wantActivity, *result.Report.ActivityClaim())
		}
	}
}

func TestHandleHookUnsupportedHarness(t *testing.T) {
	t.Parallel()

	var rawPayload json.RawMessage
	var payload map[string]any
	if _, ok := HandleHook(registry.Harness("codex"), "Stop", rawPayload, payload, nil); ok {
		t.Fatal("expected codex to have no managed hook adapter")
	}
}
