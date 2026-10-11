package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const (
	openclawLaunchSession       = "aht-fakeprovider"
	openclawLaunchContextWindow = 32000
	openclawLaunchMaxTokens     = 4096
)

var errNotTCPAddress = errors.New("reserved local address is not TCP")

func (openclawHarness) LaunchPlan(paths harness.LaunchPaths, model harness.LaunchModel) (harness.LaunchPlan, error) {
	port, err := reserveLocalPort()
	if err != nil {
		return harness.LaunchPlan{}, err
	}
	gatewayAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	modelRef := harness.LaunchProviderName + "/" + model.Model
	return harness.LaunchPlan{
		Executable: openclawCommand,
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env:        nil,
		Files: []harness.LaunchFile{{
			Path: filepath.Join(paths.Home, ".openclaw", "openclaw.json"),
			Update: func(existing []byte) ([]byte, error) {
				return openclawLaunchConfig(existing, paths.Work, model, port)
			},
		}},
		Setup: nil,
		Services: []harness.LaunchService{{
			Args:    []string{"gateway", "run", "--bind", "loopback", "--port", strconv.Itoa(port), "--auth", "none"},
			Address: gatewayAddress,
		}},
		Headless: func(prompt string) ([]string, error) {
			return []string{"agent", "--session-id", openclawLaunchSession, "--model", modelRef, "--message", prompt, "--timeout", "30"}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{"tui", "--session", openclawLaunchSession}
			if prompt != "" {
				args = append(args, "--message", prompt)
			}
			return args, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "exec",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}

func reserveLocalPort() (int, error) {
	var config net.ListenConfig
	listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve OpenClaw gateway port: %w", err)
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("release OpenClaw gateway port: %w", err)
	}
	if !ok {
		return 0, errNotTCPAddress
	}
	return address.Port, nil
}

func openclawLaunchConfig(existing []byte, work string, model harness.LaunchModel, port int) ([]byte, error) {
	settings := make(map[string]any)
	if existing != nil {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, fmt.Errorf("decode OpenClaw config: %w", err)
		}
	}
	defaults := mapValue(mapValue(settings, "agents"), "defaults")
	defaults["workspace"] = work
	defaults["model"] = map[string]any{"primary": harness.LaunchProviderName + "/" + model.Model}
	providers := mapValue(mapValue(settings, "models"), "providers")
	providers[harness.LaunchProviderName] = map[string]any{
		"baseUrl": model.Endpoint + "/v1",
		"apiKey":  model.APIKey,
		"api":     "openai-completions",
		"models": []any{map[string]any{
			"id": model.Model, "name": "AHT fakeprovider", "reasoning": false,
			"input": []string{"text"}, "contextWindow": openclawLaunchContextWindow, "maxTokens": openclawLaunchMaxTokens,
		}},
	}
	plugins := mapValue(settings, "plugins")
	plugins["allow"] = []string{openclawPluginName}
	entries := mapValue(plugins, "entries")
	entries["fireworks"] = map[string]any{"enabled": false}
	entries["perplexity"] = map[string]any{"enabled": false}
	settings["gateway"] = map[string]any{
		"mode": "local",
		"port": port,
		"auth": map[string]any{"mode": "none"},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode OpenClaw config: %w", err)
	}
	return data, nil
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
