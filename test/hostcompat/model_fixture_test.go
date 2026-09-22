//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func (host isolatedHost) configureKimiModel(t *testing.T, baseURL string) {
	t.Helper()

	path := filepath.Join(host.root, "kimi", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("default_model = \"aht-compat\"\ntelemetry = false\n\n[providers.aht-compat]\ntype = \"openai_responses\"\nbase_url = %q\napi_key = \"compat\"\n\n[models.aht-compat]\nprovider = \"aht-compat\"\nmodel = \"compat\"\nmax_context_size = 128000\n\n%s", baseURL, string(data))
	host.writeFile(t, path, config)
}

func (host isolatedHost) configureDroidModel(t *testing.T, baseURL string) {
	t.Helper()

	path := filepath.Join(host.home, ".factory", "settings.json")
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
	}
	settings["customModels"] = []any{map[string]any{
		"model":           "compat",
		"displayName":     "aht-compat",
		"baseUrl":         baseURL,
		"apiKey":          "compat",
		"provider":        "generic-chat-completion-api",
		"maxOutputTokens": 4096,
	}}
	updated, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	host.writeFile(t, path, string(updated))
}

func (host isolatedHost) configureOpenClawModel(t *testing.T, baseURL string) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(host.home, ".openclaw", "openclaw.json")
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
	}
	defaults := mapValue(mapValue(settings, "agents"), "defaults")
	defaults["workspace"] = host.work
	defaults["model"] = map[string]any{"primary": "aht-compat/compat"}
	providers := mapValue(mapValue(settings, "models"), "providers")
	providers["aht-compat"] = map[string]any{
		"baseUrl": baseURL,
		"apiKey":  "compat",
		"api":     "openai-completions",
		"models": []any{map[string]any{
			"id": "compat", "name": "AHT compatibility", "reasoning": false,
			"input": []string{"text"}, "contextWindow": 32000, "maxTokens": 4096,
		}},
	}
	plugins := mapValue(settings, "plugins")
	plugins["allow"] = []string{"aht-state"}
	entries := mapValue(plugins, "entries")
	entries["fireworks"] = map[string]any{"enabled": false}
	entries["perplexity"] = map[string]any{"enabled": false}
	settings["gateway"] = map[string]any{
		"mode": "local",
		"port": port,
		"auth": map[string]any{"mode": "none"},
	}
	updated, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	host.writeFile(t, path, string(updated))
	return port
}

func mapValue(parent map[string]any, key string) map[string]any {
	value, ok := parent[key].(map[string]any)
	if ok {
		return value
	}
	value = make(map[string]any)
	parent[key] = value
	return value
}

func piModelsJSON(baseURL string) string {
	return fmt.Sprintf(`{"providers":{"aht-compat":{"baseUrl":%q,"api":"openai-completions","apiKey":"compat","compat":{"supportsDeveloperRole":false,"supportsReasoningEffort":false},"models":[{"id":"compat","name":"AHT compatibility","reasoning":false,"input":["text"],"contextWindow":32000,"maxTokens":4096}]}}}`, baseURL)
}

func ompModelsYAML(baseURL string) string {
	return fmt.Sprintf("providers:\n  aht-compat:\n    baseUrl: %q\n    api: openai-completions\n    apiKey: compat\n    models:\n      - id: compat\n        name: AHT compatibility\n        reasoning: false\n        input: [text]\n        contextWindow: 32000\n        maxTokens: 4096\n", baseURL)
}

func opencodeConfigJSON(baseURL string) string {
	return fmt.Sprintf(`{"$schema":"https://opencode.ai/config.json","provider":{"aht-compat":{"npm":"@ai-sdk/openai-compatible","name":"AHT compatibility","options":{"baseURL":%q,"apiKey":"compat"},"models":{"compat":{"name":"AHT compatibility"}}}}}`, baseURL)
}
