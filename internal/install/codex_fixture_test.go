package install

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errCodexFixtureFailure = errors.New("native Codex fixture failure")

//nolint:tagliatelle // Codex's native JSON-RPC protocol uses camelCase field names.
type codexFixtureWriteParams struct {
	FilePath         string `json:"filePath"`
	ReloadUserConfig bool   `json:"reloadUserConfig"`
	Edits            []struct {
		KeyPath       string         `json:"keyPath"`
		MergeStrategy string         `json:"mergeStrategy"`
		Value         map[string]any `json:"value"`
	} `json:"edits"`
}

type codexFixtureCommand struct {
	Command string `json:"command"`
	Type    string `json:"type"`
}

func installFakeCodexCLI(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "config.json")
	writeCodexFixtureConfig(t, state, map[string]any{
		"model": "fixture-model",
		"hooks": map[string]any{"enabled": false, "state": map[string]any{
			"unrelated-native-key": map[string]any{"trusted_hash": "sha256:unrelated", "enabled": false},
		}},
	})
	wrapper := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestCodexNativeCLIProcess$ -- \"$@\"\n"
	wrapperPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wrapperPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHT_TEST_CODEX_PROCESS", "1")
	t.Setenv("AHT_TEST_CODEX_CONFIG", state)
	t.Setenv("AHT_TEST_CODEX_FAILURE", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

func writeCodexFixtureConfig(t *testing.T, path string, config map[string]any) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCodexFixtureConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestCodexNativeCLIProcess(t *testing.T) {
	if os.Getenv("AHT_TEST_CODEX_PROCESS") != "1" {
		return
	}
	if err := serveCodexFixture(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func serveCodexFixture() error {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode fixture request: %w", err)
		}
		if len(request.ID) == 0 {
			continue
		}
		result, err := codexFixtureResponse(request.Method, request.Params)
		response := map[string]any{"id": request.ID, "result": result}
		if err != nil {
			delete(response, "result")
			response["error"] = map[string]any{"code": -32000, "message": err.Error()}
		}
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("encode fixture response: %w", err)
		}
	}
}

func codexFixtureResponse(method string, params json.RawMessage) (any, error) {
	failure := os.Getenv("AHT_TEST_CODEX_FAILURE")
	if failure == "rpc" {
		return nil, fmt.Errorf("%w: RPC unavailable", errCodexFixtureFailure)
	}
	if method == "initialize" {
		return map[string]any{"userAgent": "fixture", "platformFamily": "unix", "platformOs": "linux"}, nil
	}
	configPath := os.Getenv("AHT_TEST_CODEX_CONFIG")
	config, state, err := loadCodexFixtureConfig(configPath)
	if err != nil {
		return nil, err
	}
	switch method {
	case "hooks/list":
		return listCodexFixtureHooks(params, state, failure)
	case "config/batchWrite":
		return writeCodexFixtureApproval(params, configPath, config, state, failure)
	default:
		return nil, fmt.Errorf("%w: unsupported method %q", errCodexFixtureFailure, method)
	}
}

func loadCodexFixtureConfig(path string) (map[string]any, map[string]any, error) {
	data, err := readCodexFixtureFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read fixture config: %w", err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, nil, fmt.Errorf("decode fixture config: %w", err)
	}
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%w: config hooks must be an object", errCodexFixtureFailure)
	}
	state, ok := hooks["state"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%w: config hook state must be an object", errCodexFixtureFailure)
	}
	return config, state, nil
}

func listCodexFixtureHooks(params json.RawMessage, state map[string]any, failure string) (any, error) {
	var input struct {
		CWDs []string `json:"cwds"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("decode fixture hook list parameters: %w", err)
	}
	metadata, err := codexFixtureHooks(state)
	if err != nil {
		return nil, err
	}
	if failure == "missing command" && len(metadata) > 0 {
		metadata = metadata[1:]
	}
	entries := make([]any, 0, len(input.CWDs))
	for _, cwd := range input.CWDs {
		discoveryErrors := []map[string]any{}
		if failure == "discovery" {
			discoveryErrors = append(discoveryErrors, map[string]any{"message": "fixture discovery failed", "path": filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")})
		}
		entries = append(entries, map[string]any{"cwd": cwd, "hooks": metadata, "warnings": []string{}, "errors": discoveryErrors})
	}
	return map[string]any{"data": entries}, nil
}

func writeCodexFixtureApproval(params json.RawMessage, configPath string, config, state map[string]any, failure string) (any, error) {
	if failure == "write" {
		return nil, fmt.Errorf("%w: config is unwritable", errCodexFixtureFailure)
	}
	var input codexFixtureWriteParams
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, fmt.Errorf("decode fixture approval parameters: %w", err)
	}
	if input.FilePath != filepath.Join(os.Getenv("CODEX_HOME"), "config.toml") || !input.ReloadUserConfig {
		return nil, fmt.Errorf("%w: approval must target the isolated user config and reload it", errCodexFixtureFailure)
	}
	for _, edit := range input.Edits {
		if edit.KeyPath != "hooks.state" || edit.MergeStrategy != "upsert" {
			return nil, fmt.Errorf("%w: unexpected config mutation %q", errCodexFixtureFailure, edit.KeyPath)
		}
		if failure != "verification" {
			upsertCodexFixtureConfig(state, edit.Value)
		}
	}
	updated, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode fixture approval config: %w", err)
	}
	if err := writeCodexFixtureFile(configPath, updated); err != nil {
		return nil, fmt.Errorf("write fixture approval config: %w", err)
	}
	return map[string]any{"status": "ok", "version": "fixture-version", "filePath": input.FilePath, "overriddenMetadata": nil}, nil
}

func upsertCodexFixtureConfig(config, update map[string]any) {
	for key, value := range update {
		existing, existingMap := config[key].(map[string]any)
		incoming, incomingMap := value.(map[string]any)
		if existingMap && incomingMap {
			upsertCodexFixtureConfig(existing, incoming)
			continue
		}
		config[key] = value
	}
}

func readCodexFixtureFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open fixture read root: %w", err)
	}
	data, readErr := root.ReadFile(filepath.Base(path))
	if err := errors.Join(readErr, root.Close()); err != nil {
		return nil, fmt.Errorf("read rooted fixture file: %w", err)
	}
	return data, nil
}

func writeCodexFixtureFile(path string, data []byte) (writeErr error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open fixture write root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			writeErr = errors.Join(writeErr, fmt.Errorf("close fixture write root: %w", err))
		}
	}()
	if err := root.WriteFile(filepath.Base(path), data, 0o600); err != nil {
		return fmt.Errorf("write rooted fixture file: %w", err)
	}
	return nil
}

func codexFixtureHooks(state map[string]any) ([]map[string]any, error) {
	path := filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve fixture hooks: %w", err)
	}
	data, err := readCodexFixtureFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read fixture hooks: %w", err)
	}
	var config struct {
		Hooks map[string][]struct {
			Matcher any                   `json:"matcher"`
			Hooks   []codexFixtureCommand `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode fixture hooks: %w", err)
	}
	events := []struct{ JSON, Native, Key string }{
		{"SessionStart", "sessionStart", "session_start"},
		{"UserPromptSubmit", "userPromptSubmit", "user_prompt_submit"},
		{"PostToolUse", "postToolUse", "post_tool_use"},
		{"PreCompact", "preCompact", "pre_compact"},
		{"PostCompact", "postCompact", "post_compact"},
		{"SessionEnd", "sessionEnd", "session_end"},
		{"Stop", "stop", "stop"},
		{"PermissionRequest", "permissionRequest", "permission_request"},
		{"Interrupt", "interrupt", "interrupt"},
	}
	metadata := []map[string]any{}
	for _, event := range events {
		for groupIndex, group := range config.Hooks[event.JSON] {
			for hookIndex, hook := range group.Hooks {
				key := fmt.Sprintf("%s:%s:%d:%d", path, event.Key, groupIndex, hookIndex)
				metadata = append(metadata, codexFixtureHookMetadata(path, event.Native, key, group.Matcher, hook, state))
			}
		}
	}
	return metadata, nil
}

func codexFixtureHookMetadata(path, event, key string, matcher any, hook codexFixtureCommand, state map[string]any) map[string]any {
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(event+"\x00"+hook.Command)))
	status := codexFixtureTrustStatus(state[key], hash)
	if os.Getenv("AHT_TEST_CODEX_FAILURE") == "hash" {
		hash = ""
	}
	return map[string]any{"key": key, "eventName": event, "handlerType": hook.Type, "command": hook.Command, "sourcePath": path, "source": "user", "currentHash": hash, "trustStatus": status, "matcher": matcher, "async": false, "timeoutSec": 5, "statusMessage": "fixture"}
}

func codexFixtureTrustStatus(approval any, hash string) string {
	approved, ok := approval.(map[string]any)
	if !ok {
		return "untrusted"
	}
	if approved["trusted_hash"] == hash {
		return "trusted"
	}
	return "modified"
}
