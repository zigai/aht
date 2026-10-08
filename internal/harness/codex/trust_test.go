package codex_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/codex"
)

const (
	nativeTrustHash        = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	changedNativeTrustHash = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

var nativeTrustKeys = []string{
	"session_start:4:2", "session_start:9:3", "user_prompt_submit:7:1",
	"permission_request:3:4", "post_tool_use:8:2", "pre_compact:6:1",
	"post_compact:5:3", "stop:2:8", "interrupt:7:6", "session_end:3:9",
}

func TestCodexTrustInstallUsesNativeHashesAndOnlyOwnedHooks(t *testing.T) {
	f := newNativeTrustFixture(t)
	before := f.state(t)

	if err := f.plan.Trust.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	writes := f.writes(t)
	if len(writes) != 1 {
		t.Fatalf("native writes = %d, want one subset update", len(writes))
	}
	want := f.ownedState(nativeTrustHash)
	assertNativeTrustWrite(t, f.home, writes[0], want)
	after := f.state(t)
	for key, value := range want {
		if before[key] == nil {
			before[key] = make(map[string]any)
		}
		maps.Copy(before[key], value)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("native state = %#v, want %#v (foreign and disabled entries preserved)", after, before)
	}
	f.assertConfigUntouched(t)

	trusted, err := f.plan.Trust.Inspect(t.Context())
	if err != nil || !trusted {
		t.Fatalf("Inspect after install = %v, %v, want trusted", trusted, err)
	}
	if err := f.plan.Trust.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.writes(t)); got != 1 {
		t.Fatalf("writes after repeated install and inspection = %d, want unchanged", got)
	}
}

func TestCodexTrustUpdateWritesOnlyChangedOwnedHashes(t *testing.T) {
	f := newNativeTrustFixture(t)
	state := f.state(t)
	maps.Copy(state, f.ownedState(nativeTrustHash))
	f.saveState(t, state)
	f.hooks[3]["currentHash"] = changedNativeTrustHash
	f.hooks[8]["currentHash"] = changedNativeTrustHash
	f.saveFixture(t)

	if err := f.plan.Trust.Install(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		f.key("permission_request:3:4"): {"trusted_hash": changedNativeTrustHash},
		f.key("interrupt:7:6"):          {"trusted_hash": changedNativeTrustHash},
	}
	writes := f.writes(t)
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want one subset update", len(writes))
	}
	assertNativeTrustWrite(t, f.home, writes[0], want)
	maps.Copy(state, want)
	if got := f.state(t); !reflect.DeepEqual(got, state) {
		t.Fatalf("updated state = %#v, want %#v", got, state)
	}
}

func TestCodexTrustInspectIsReadOnly(t *testing.T) {
	for _, status := range []string{"trusted", "untrusted", "modified"} {
		t.Run(status, func(t *testing.T) {
			f := newNativeTrustFixture(t)
			state := f.state(t)
			maps.Copy(state, f.ownedState(nativeTrustHash))
			if status != "trusted" {
				delete(state, f.key("session_end:3:9"))
				f.hooks[9]["trustStatus"] = status
			}
			f.saveState(t, state)
			f.saveFixture(t)

			got, err := f.plan.Trust.Inspect(t.Context())
			if err != nil || got != (status == "trusted") {
				t.Fatalf("Inspect = %v, %v; native status %s", got, err, status)
			}
			if len(f.writes(t)) != 0 || !reflect.DeepEqual(f.state(t), state) {
				t.Fatal("Inspect mutated native trust state")
			}
			f.assertConfigUntouched(t)
		})
	}
}

func TestCodexTrustInstallRejectsIncompleteOrInvalidDiscovery(t *testing.T) {
	cases := []struct {
		name   string
		change func(*nativeTrustFixture)
	}{
		{"missing native hash", func(f *nativeTrustFixture) { delete(f.hooks[4], "currentHash") }},
		{"empty native hash", func(f *nativeTrustFixture) { f.hooks[4]["currentHash"] = "" }},
		{"invalid native hash", func(f *nativeTrustFixture) { f.hooks[4]["currentHash"] = "not-a-native-hash" }},
		{"missing native key", func(f *nativeTrustFixture) { delete(f.hooks[4], "key") }},
		{"wrong native key source", func(f *nativeTrustFixture) { f.hooks[4]["key"] = "/other/hooks.json:post_tool_use:8:2" }},
		{"missing trust status", func(f *nativeTrustFixture) { delete(f.hooks[4], "trustStatus") }},
		{"unknown trust status", func(f *nativeTrustFixture) { f.hooks[4]["trustStatus"] = "unexpected" }},
		{"wrong command", func(f *nativeTrustFixture) { f.hooks[4]["command"] = "other-agent report" }},
		{"project source", func(f *nativeTrustFixture) { f.hooks[4]["source"] = "project" }},
		{"different source path", func(f *nativeTrustFixture) { f.hooks[4]["sourcePath"] = filepath.Join(f.home, "project", "hooks.json") }},
		{"noncommand handler", func(f *nativeTrustFixture) { f.hooks[4]["handlerType"] = "prompt" }},
		{"discovery errors", func(f *nativeTrustFixture) { f.mode = "discovery-errors" }},
		{"malformed discovery", func(f *nativeTrustFixture) { f.mode = "malformed-discovery" }},
		{"discovery RPC error", func(f *nativeTrustFixture) { f.mode = "discovery-rpc-error" }},
		{"initialization RPC error", func(f *nativeTrustFixture) { f.mode = "initialize-error" }},
		{"process exit", func(f *nativeTrustFixture) { f.mode = "exit" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newNativeTrustFixture(t)
			before := f.state(t)
			tc.change(f)
			f.saveFixture(t)
			if err := f.plan.Trust.Install(t.Context()); err == nil {
				t.Fatal("Install succeeded despite invalid native discovery")
			}
			if len(f.writes(t)) != 0 || !reflect.DeepEqual(f.state(t), before) {
				t.Fatal("invalid discovery changed trust state")
			}
			f.assertConfigUntouched(t)
		})
	}
}

func TestCodexTrustInstallRequiresEveryExpectedHook(t *testing.T) {
	for missing := range 10 {
		t.Run(nativeTrustKeys[missing], func(t *testing.T) {
			f := newNativeTrustFixture(t)
			before := f.state(t)
			f.hooks = append(f.hooks[:missing], f.hooks[missing+1:]...)
			f.saveFixture(t)
			if trusted, err := f.plan.Trust.Inspect(t.Context()); err != nil || trusted {
				t.Fatalf("Inspect with expected hook missing = %v, %v, want false without error", trusted, err)
			}
			if err := f.plan.Trust.Install(t.Context()); err == nil {
				t.Fatal("Install succeeded with an expected hook missing")
			}
			if len(f.writes(t)) != 0 || !reflect.DeepEqual(f.state(t), before) {
				t.Fatal("partial expected hook discovery changed trust state")
			}
		})
	}
}

func TestCodexTrustInspectPropagatesDiscoveryFailures(t *testing.T) {
	for _, mode := range []string{"discovery-errors", "malformed-discovery", "discovery-rpc-error", "initialize-error", "exit"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeTrustFixture(t)
			before := f.state(t)
			f.mode = mode
			f.saveFixture(t)
			trusted, err := f.plan.Trust.Inspect(t.Context())
			if err == nil || trusted {
				t.Fatalf("Inspect = %v, %v, want discovery failure", trusted, err)
			}
			if len(f.writes(t)) != 0 || !reflect.DeepEqual(f.state(t), before) {
				t.Fatal("failed inspection changed trust state")
			}
			f.assertConfigUntouched(t)
		})
	}
}

func TestCodexTrustInstallPropagatesWriteAndVerificationFailures(t *testing.T) {
	for _, mode := range []string{"write-error", "verification-untrusted", "verification-rpc-error"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeTrustFixture(t)
			before := f.state(t)
			f.mode = mode
			f.saveFixture(t)
			if err := f.plan.Trust.Install(t.Context()); err == nil {
				t.Fatal("Install succeeded despite failed write or verification")
			}
			if len(f.writes(t)) != 1 {
				t.Fatalf("writes = %d, want attempted native update", len(f.writes(t)))
			}
			if mode == "write-error" && !reflect.DeepEqual(f.state(t), before) {
				t.Fatal("failed native write changed state")
			}
			f.assertConfigUntouched(t)
		})
	}
}

func TestCodexTrustMissingExecutableFails(t *testing.T) {
	f := newNativeTrustFixture(t)
	t.Setenv("PATH", t.TempDir())
	if err := f.plan.Trust.Install(t.Context()); err == nil {
		t.Fatal("Install succeeded without Codex")
	}
	if _, err := f.plan.Trust.Inspect(t.Context()); err == nil {
		t.Fatal("Inspect succeeded without Codex")
	}
	if len(f.writes(t)) != 0 {
		t.Fatal("missing Codex caused a native write")
	}
}

type nativeTrustFixture struct {
	home   string
	root   *os.Root
	plan   harness.JSONCommandHookInstallPlan
	hooks  []map[string]any
	mode   string
	config []byte
}

func newNativeTrustFixture(t *testing.T) *nativeTrustFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	action, ok := codex.New().InstallPlan("/opt/example agent/bin/aht").Actions[0].(harness.JSONCommandHooksAction)
	if !ok {
		t.Fatal("Codex install plan does not contain a command hook action")
	}
	plan := action.Plan
	if plan.Trust == nil {
		t.Fatal("Codex install plan has no native hook trust implementation")
	}
	if len(plan.Hooks) != 10 {
		t.Fatalf("fixture requires all ten expected hooks, got %d", len(plan.Hooks))
	}
	f := &nativeTrustFixture{home: home, plan: plan, config: []byte("model = \"example-model\"\n[hooks]\nenabled = false\n[features]\nexample_feature = true\n")}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	f.root = root
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	events := []string{"sessionStart", "sessionStart", "userPromptSubmit", "permissionRequest", "postToolUse", "preCompact", "postCompact", "stop", "interrupt", "sessionEnd"}
	for i, spec := range plan.Hooks {
		f.hooks = append(f.hooks, map[string]any{
			"key": f.key(nativeTrustKeys[i]), "eventName": events[i], "handlerType": "command",
			"command": spec.Command, "source": "user", "sourcePath": filepath.Join(home, "hooks.json"),
			"currentHash": nativeTrustHash, "trustStatus": "untrusted", "enabled": true,
			"async": false, "matcher": spec.Matcher, "timeoutSec": 5,
		})
	}
	f.hooks = append(f.hooks, map[string]any{
		"key": f.key("session_start:1:0"), "eventName": "sessionStart", "handlerType": "command",
		"command": "unrelated-agent run", "source": "user", "sourcePath": filepath.Join(home, "hooks.json"),
		"currentHash": changedNativeTrustHash, "trustStatus": "untrusted",
	})
	f.addLookalikes()
	f.saveFixture(t)
	f.saveState(t, map[string]map[string]any{
		f.key("session_start:1:0"):      {"trusted_hash": "foreign-hash", "disabled": true},
		"foreign-plugin:stop:0:0":       {"trusted_hash": "plugin-hash", "disabled": true},
		f.key("permission_request:3:4"): {"disabled": true},
	})
	if err := f.root.WriteFile("config.toml", f.config, 0o600); err != nil {
		t.Fatal(err)
	}
	f.installProcess(t)
	return f
}

func (f *nativeTrustFixture) addLookalikes() {
	for i, field := range []string{"source", "sourcePath", "command", "handlerType"} {
		lookalike := maps.Clone(f.hooks[0])
		lookalike["key"] = f.key(fmt.Sprintf("session_start:20:%d", i))
		switch field {
		case "source":
			lookalike[field] = "project"
		case "sourcePath":
			lookalike[field] = filepath.Join(f.home, "other", "hooks.json")
		case "command":
			lookalike[field] = f.plan.Hooks[0].Command + " --unrelated"
		case "handlerType":
			lookalike[field] = "prompt"
		}
		f.hooks = append(f.hooks, lookalike)
	}
}

func (f *nativeTrustFixture) installProcess(t *testing.T) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHT_NATIVE_TRUST_HELPER", "1")
	t.Setenv("AHT_NATIVE_TRUST_TEST_BINARY", binary)
	shim := "#!/bin/sh\nexec \"$AHT_NATIVE_TRUST_TEST_BINARY\" -test.run=^TestCodexNativeTrustProcess$ -- \"$@\"\n"
	if err := f.root.WriteFile("codex", []byte(shim), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.root.Chmod("codex", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", f.home+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (f *nativeTrustFixture) key(suffix string) string {
	return filepath.Join(f.home, "hooks.json") + ":" + suffix
}

func (f *nativeTrustFixture) ownedState(hash string) map[string]map[string]any {
	state := make(map[string]map[string]any)
	for _, suffix := range nativeTrustKeys {
		state[f.key(suffix)] = map[string]any{"trusted_hash": hash}
	}
	return state
}

func (f *nativeTrustFixture) saveFixture(t *testing.T) {
	t.Helper()
	writeNativeTrustJSON(t, f.root, "fixture.json", map[string]any{"hooks": f.hooks, "mode": f.mode})
}

func (f *nativeTrustFixture) saveState(t *testing.T, state map[string]map[string]any) {
	t.Helper()
	writeNativeTrustJSON(t, f.root, "state.json", state)
}

func (f *nativeTrustFixture) state(t *testing.T) map[string]map[string]any {
	t.Helper()
	var state map[string]map[string]any
	readNativeTrustJSON(t, f.root, "state.json", &state)
	return state
}

func (f *nativeTrustFixture) writes(t *testing.T) []nativeTrustWrite {
	t.Helper()
	data, err := f.root.ReadFile("writes.jsonl")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var writes []nativeTrustWrite
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var w nativeTrustWrite
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			t.Fatal(err)
		}
		writes = append(writes, w)
	}
	return writes
}

func (f *nativeTrustFixture) assertConfigUntouched(t *testing.T) {
	t.Helper()
	got, err := f.root.ReadFile("config.toml")
	if err != nil || string(got) != string(f.config) {
		t.Fatalf("config was modified outside native subset write: %q, %v", got, err)
	}
}

//nolint:tagliatelle // Codex app-server requires these camelCase wire names.
type nativeTrustWrite struct {
	FilePath         string `json:"filePath"`
	ReloadUserConfig bool   `json:"reloadUserConfig"`
	Edits            []struct {
		KeyPath       string                    `json:"keyPath"`
		MergeStrategy string                    `json:"mergeStrategy"`
		Value         map[string]map[string]any `json:"value"`
	} `json:"edits"`
}

func assertNativeTrustWrite(t *testing.T, home string, got nativeTrustWrite, want map[string]map[string]any) {
	t.Helper()
	if got.FilePath != filepath.Join(home, "config.toml") || !got.ReloadUserConfig {
		t.Fatalf("native write target/reload = %q/%v", got.FilePath, got.ReloadUserConfig)
	}
	if len(got.Edits) != 1 || got.Edits[0].KeyPath != "hooks.state" || got.Edits[0].MergeStrategy != "upsert" {
		t.Fatalf("native edits = %#v, want only hooks.state upsert", got.Edits)
	}
	if !reflect.DeepEqual(got.Edits[0].Value, want) {
		t.Fatalf("trusted subset = %#v, want native keys and hashes %#v", got.Edits[0].Value, want)
	}
}

func writeNativeTrustJSON(t *testing.T, root *os.Root, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readNativeTrustJSON(t *testing.T, root *os.Root, name string, value any) {
	t.Helper()
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}

type nativeTrustRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type nativeTrustServer struct {
	root        *os.Root
	home        string
	hooks       []map[string]any
	mode        string
	state       map[string]map[string]any
	initialized bool
	notified    bool
	wrote       bool
	writer      *json.Encoder
}

func TestCodexNativeTrustProcess(t *testing.T) {
	if os.Getenv("AHT_NATIVE_TRUST_HELPER") != "1" {
		return
	}
	args := os.Args
	if len(args) < 4 || !reflect.DeepEqual(args[len(args)-3:], []string{"app-server", "--listen", "stdio://"}) {
		os.Exit(2)
	}
	home := os.Getenv("CODEX_HOME")
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Hooks []map[string]any `json:"hooks"`
		Mode  string           `json:"mode"`
	}
	readNativeTrustJSON(t, root, "fixture.json", &fixture)
	if fixture.Mode == "exit" {
		os.Exit(3)
	}
	var state map[string]map[string]any
	readNativeTrustJSON(t, root, "state.json", &state)
	server := nativeTrustServer{root: root, home: home, hooks: fixture.Hooks, mode: fixture.Mode, state: state, writer: json.NewEncoder(os.Stdout)}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request nativeTrustRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(4)
		}
		server.handle(t, request)
	}
	if scanner.Err() != nil {
		os.Exit(9)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func (s *nativeTrustServer) respond(request nativeTrustRequest, result any, failure bool) {
	response := map[string]any{"id": request.ID}
	if failure {
		response["error"] = map[string]any{"code": -32000, "message": "native fixture failure"}
	} else {
		response["result"] = result
	}
	if err := s.writer.Encode(response); err != nil {
		os.Exit(5)
	}
}

func (s *nativeTrustServer) handle(t *testing.T, request nativeTrustRequest) {
	t.Helper()
	switch request.Method {
	case "initialize":
		s.initialize(request)
	case "initialized":
		s.notified = s.initialized && len(request.ID) == 0
	case "hooks/list":
		s.list(request)
	case "config/batchWrite":
		s.write(t, request)
	default:
		s.respond(request, nil, true)
	}
}

func (s *nativeTrustServer) initialize(request nativeTrustRequest) {
	var params struct {
		Capabilities struct {
			ExperimentalAPI bool `json:"experimentalApi"` //nolint:tagliatelle // Codex app-server requires camelCase.
		} `json:"capabilities"`
	}
	if json.Unmarshal(request.Params, &params) != nil || !params.Capabilities.ExperimentalAPI {
		s.respond(request, nil, true)
		return
	}
	s.initialized = true
	s.respond(request, map[string]any{"userAgent": "native-test", "platformFamily": "unix", "platformOs": "linux"}, s.mode == "initialize-error")
}

func (s *nativeTrustServer) list(request nativeTrustRequest) {
	var params struct {
		CWDs []string `json:"cwds"`
	}
	if !s.notified || json.Unmarshal(request.Params, &params) != nil || len(params.CWDs) != 1 || !filepath.IsAbs(params.CWDs[0]) {
		s.respond(request, nil, true)
		return
	}
	if s.mode == "discovery-rpc-error" || (s.wrote && s.mode == "verification-rpc-error") {
		s.respond(request, nil, true)
		return
	}
	if s.mode == "malformed-discovery" {
		s.respond(request, map[string]any{"data": "not-hook-rows"}, false)
		return
	}
	s.refreshTrustStatuses()
	errors := []any{}
	if s.mode == "discovery-errors" {
		errors = append(errors, map[string]any{"path": filepath.Join(s.home, "hooks.json"), "message": "invalid hooks config"})
	}
	s.respond(request, map[string]any{"data": []any{map[string]any{"cwd": params.CWDs[0], "hooks": s.hooks, "warnings": []any{}, "errors": errors}}}, false)
}

func (s *nativeTrustServer) refreshTrustStatuses() {
	for _, hook := range s.hooks {
		key, _ := hook["key"].(string)
		if hash, ok := s.state[key]["trusted_hash"]; ok && hash == hook["currentHash"] {
			hook["trustStatus"] = "trusted"
		}
		if s.wrote && s.mode == "verification-untrusted" {
			hook["trustStatus"] = "untrusted"
		}
	}
}

func (s *nativeTrustServer) write(t *testing.T, request nativeTrustRequest) {
	t.Helper()
	var params nativeTrustWrite
	if !s.notified || json.Unmarshal(request.Params, &params) != nil {
		s.respond(request, nil, true)
		return
	}
	log, err := s.root.OpenFile("writes.jsonl", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(6)
	}
	if err := json.NewEncoder(log).Encode(params); err != nil {
		os.Exit(7)
	}
	if err := log.Close(); err != nil {
		os.Exit(8)
	}
	if s.mode == "write-error" {
		s.respond(request, nil, true)
		return
	}
	if !params.isTrustSubsetWrite(s.home) {
		s.respond(request, nil, true)
		return
	}
	for key, fields := range params.Edits[0].Value {
		if s.state[key] == nil {
			s.state[key] = make(map[string]any)
		}
		maps.Copy(s.state[key], fields)
	}
	writeNativeTrustJSON(t, s.root, "state.json", s.state)
	s.wrote = true
	s.respond(request, map[string]any{"status": "ok", "version": "test-version", "filePath": params.FilePath, "overriddenMetadata": nil}, false)
}

func (w nativeTrustWrite) isTrustSubsetWrite(home string) bool {
	return w.FilePath == filepath.Join(home, "config.toml") &&
		w.ReloadUserConfig &&
		len(w.Edits) == 1 &&
		w.Edits[0].KeyPath == "hooks.state" &&
		w.Edits[0].MergeStrategy == "upsert"
}
