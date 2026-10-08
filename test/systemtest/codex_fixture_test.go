//go:build integration

package systemtest

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const releaseUserCodexCommand = "/bin/true release-user-stop"

type releaseCodexFixture struct {
	home  string
	state string
}

type releaseCodexHook struct {
	key, event, command, kind, hash string
	matcher                         any
}

func installReleaseCodexCLI(t *testing.T, environment []string) ([]string, releaseCodexFixture) {
	t.Helper()
	var home, path string
	for _, value := range environment {
		if value, ok := strings.CutPrefix(value, "HOME="); ok {
			home = value
		}
		if value, ok := strings.CutPrefix(value, "PATH="); ok {
			path = value
		}
	}
	dir := t.TempDir()
	fixture := releaseCodexFixture{home: filepath.Join(home, ".codex"), state: filepath.Join(dir, "state.json")}
	if err := os.MkdirAll(fixture.home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeReleaseCodexJSON(t, fixture.state, map[string]map[string]any{
		fixture.userKey(): {"trusted_hash": "sha256:user-original", "disabled": true},
		"foreign-plugin":  {"trusted_hash": "sha256:foreign", "disabled": true},
	})
	if err := os.WriteFile(filepath.Join(fixture.home, "hooks.json"), []byte(`{"hooks":{"Stop":[{"matcher":"*","hooks":[{"type":"command","command":"`+releaseUserCodexCommand+`"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestReleaseCodexCLIProcess$ -- \"$@\"\n"
	wrapperPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wrapperPath, 0o700); err != nil {
		t.Fatal(err)
	}
	environment = append(environment, "PATH="+dir+string(os.PathListSeparator)+path,
		"AHT_TEST_RELEASE_CODEX=1", "AHT_TEST_RELEASE_CODEX_STATE="+fixture.state, "CODEX_HOME="+fixture.home)
	return environment, fixture
}

func (fixture releaseCodexFixture) userKey() string {
	return filepath.Join(fixture.home, "hooks.json") + ":stop:0:0"
}

func (fixture releaseCodexFixture) setManagedTrust(t *testing.T, trusted bool) {
	t.Helper()
	var state map[string]map[string]any
	decodeJSONFile(t, fixture.state, &state)
	for _, hook := range releaseCodexHooks(t, fixture.home) {
		if hook.command == releaseUserCodexCommand {
			continue
		}
		if trusted {
			state[hook.key] = map[string]any{"trusted_hash": hook.hash}
		} else {
			delete(state, hook.key)
		}
	}
	writeReleaseCodexJSON(t, fixture.state, state)
}

func (fixture releaseCodexFixture) assertTrusted(t *testing.T) {
	t.Helper()
	var state map[string]map[string]any
	decodeJSONFile(t, fixture.state, &state)
	if !reflect.DeepEqual(state[fixture.userKey()], map[string]any{"trusted_hash": "sha256:user-original", "disabled": true}) ||
		!reflect.DeepEqual(state["foreign-plugin"], map[string]any{"trusted_hash": "sha256:foreign", "disabled": true}) {
		t.Fatalf("Codex trust changed unrelated configuration: %+v", state)
	}
	managed, user := 0, 0
	for _, hook := range releaseCodexHooks(t, fixture.home) {
		if hook.command == releaseUserCodexCommand {
			user++
			if hook.key != fixture.userKey() || hook.kind != "command" || hook.matcher != "*" {
				t.Fatalf("Codex user hook changed: %+v", hook)
			}
			continue
		}
		managed++
		if state[hook.key]["trusted_hash"] != hook.hash {
			t.Fatalf("Codex hook %q has no persisted approval for current hash %q", hook.key, hook.hash)
		}
	}
	if managed == 0 || user != 1 {
		t.Fatalf("Codex hooks after upgrade: managed=%d user=%d", managed, user)
	}
	t.Logf("Codex persisted current-hash approvals for %d managed hooks; user hook and unrelated trust entries preserved", managed)
}

func TestReleaseCodexCLIProcess(t *testing.T) {
	if os.Getenv("AHT_TEST_RELEASE_CODEX") != "1" {
		return
	}
	fixture := releaseCodexFixture{home: os.Getenv("CODEX_HOME"), state: os.Getenv("AHT_TEST_RELEASE_CODEX_STATE")}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				os.Exit(0)
			}
			t.Fatal(err)
		}
		if len(request.ID) == 0 {
			continue
		}
		var result any
		if request.Method == "initialize" {
			result = map[string]any{"userAgent": "release-fixture"}
		} else {
			result = fixture.respond(t, request.Method, request.Params)
		}
		if err := encoder.Encode(map[string]any{"id": request.ID, "result": result}); err != nil {
			t.Fatal(err)
		}
	}
}

func (fixture releaseCodexFixture) respond(t *testing.T, method string, params json.RawMessage) any {
	t.Helper()
	var state map[string]map[string]any
	decodeJSONFile(t, fixture.state, &state)
	hooks := releaseCodexHooks(t, fixture.home)
	switch method {
	case "hooks/list":
		rows := make([]map[string]any, 0, len(hooks))
		for _, hook := range hooks {
			status := "untrusted"
			if stored, exists := state[hook.key]["trusted_hash"]; exists {
				status = "modified"
				if stored == hook.hash {
					status = "trusted"
				}
			}
			rows = append(rows, map[string]any{
				"key": hook.key, "eventName": hook.event, "command": hook.command, "handlerType": hook.kind,
				"source": "user", "sourcePath": filepath.Join(fixture.home, "hooks.json"), "currentHash": hook.hash, "trustStatus": status,
			})
		}
		return map[string]any{"data": []any{map[string]any{"hooks": rows, "errors": []any{}, "warnings": []any{}}}}
	case "config/batchWrite":
		fixture.writeTrust(t, params, state, hooks)
		return map[string]any{"status": "ok"}
	default:
		t.Fatalf("unexpected Codex native method %q", method)
		return nil
	}
}

func (fixture releaseCodexFixture) writeTrust(t *testing.T, params json.RawMessage, state map[string]map[string]any, hooks []releaseCodexHook) {
	t.Helper()
	//nolint:tagliatelle // Codex native wire names.
	var input struct {
		FilePath         string `json:"filePath"`
		ReloadUserConfig bool   `json:"reloadUserConfig"`
		Edits            []struct {
			KeyPath       string                    `json:"keyPath"`
			MergeStrategy string                    `json:"mergeStrategy"`
			Value         map[string]map[string]any `json:"value"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		t.Fatal(err)
	}
	validTarget := input.FilePath == filepath.Join(fixture.home, "config.toml") && input.ReloadUserConfig
	if !validTarget || len(input.Edits) != 1 {
		t.Fatal("invalid Codex trust write target")
	}
	edit := input.Edits[0]
	if edit.KeyPath != "hooks.state" || edit.MergeStrategy != "upsert" {
		t.Fatal("Codex trust write must upsert only hooks.state")
	}
	known := make(map[string]string, len(hooks))
	for _, hook := range hooks {
		known[hook.key] = hook.hash
	}
	for key, fields := range edit.Value {
		if len(fields) != 1 || fields["trusted_hash"] != known[key] {
			t.Fatal("Codex trust write must approve an installed hook's current hash")
		}
		if state[key] == nil {
			state[key] = make(map[string]any)
		}
		state[key]["trusted_hash"] = fields["trusted_hash"]
	}
	writeReleaseCodexJSON(t, fixture.state, state)
}

func releaseCodexHooks(t *testing.T, home string) []releaseCodexHook {
	t.Helper()
	path := filepath.Join(home, "hooks.json")
	var config struct {
		Hooks map[string][]struct {
			Matcher any `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Type    string `json:"type"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	decodeJSONFile(t, path, &config)
	events := []struct{ JSON, Native, Key string }{
		{"SessionStart", "sessionStart", "session_start"},
		{"UserPromptSubmit", "userPromptSubmit", "user_prompt_submit"},
		{"PermissionRequest", "permissionRequest", "permission_request"},
		{"PostToolUse", "postToolUse", "post_tool_use"},
		{"PreCompact", "preCompact", "pre_compact"},
		{"PostCompact", "postCompact", "post_compact"},
		{"Stop", "stop", "stop"},
		{"Interrupt", "interrupt", "interrupt"},
		{"SessionEnd", "sessionEnd", "session_end"},
	}
	var hooks []releaseCodexHook
	for _, event := range events {
		for groupIndex, group := range config.Hooks[event.JSON] {
			for hookIndex, hook := range group.Hooks {
				hooks = append(hooks, releaseCodexHook{
					key: fmt.Sprintf("%s:%s:%d:%d", path, event.Key, groupIndex, hookIndex), event: event.Native,
					command: hook.Command, kind: hook.Type, matcher: group.Matcher,
					hash: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(event.Native+"\x00"+hook.Command))),
				})
			}
		}
	}
	return hooks
}

func writeReleaseCodexJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(root.WriteFile(filepath.Base(path), data, 0o600), root.Close()); err != nil {
		t.Fatal(err)
	}
}
