package codex

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

var (
	errHookDiscovery = errors.New("missing installed aht hooks in Codex discovery")
	errHookMetadata  = errors.New("invalid Codex hook trust metadata")
	errHookTrust     = errors.New("aht hooks remain untrusted in Codex")
)

type hookTrust struct {
	path  string
	hooks []harness.CommandHookInstallSpec
}

//nolint:tagliatelle // Codex app-server hook metadata uses camelCase field names.
type nativeHook struct {
	Key         string `json:"key"`
	Command     string `json:"command"`
	HandlerType string `json:"handlerType"`
	SourcePath  string `json:"sourcePath"`
	Source      string `json:"source"`
	CurrentHash string `json:"currentHash"`
	TrustStatus string `json:"trustStatus"`
}

type hooksList struct {
	Data []struct {
		Hooks  []nativeHook `json:"hooks"`
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"data"`
}

func (trust hookTrust) Install(ctx context.Context) error {
	return trust.withServer(ctx, func(server *hookServer, path string) error {
		hooks, complete, err := trust.list(server, path)
		if err != nil {
			return err
		}
		if !complete {
			return errHookDiscovery
		}
		updates := make(map[string]map[string]string)
		for _, hook := range hooks {
			if hook.TrustStatus != "trusted" {
				updates[hook.Key] = map[string]string{"trusted_hash": hook.CurrentHash}
			}
		}
		if len(updates) == 0 {
			return nil
		}
		var result struct {
			Status string `json:"status"`
		}
		if err := server.request("config/batchWrite", map[string]any{
			"edits":            []map[string]any{{"keyPath": "hooks.state", "value": updates, "mergeStrategy": "upsert"}},
			"filePath":         filepath.Join(filepath.Dir(path), "config.toml"),
			"reloadUserConfig": true,
		}, &result); err != nil {
			return err
		}
		if result.Status != "ok" {
			return fmt.Errorf("%w: config/batchWrite status %q", errHookTrust, result.Status)
		}
		hooks, complete, err = trust.list(server, path)
		if err != nil {
			return err
		}
		if !complete || !allHooksTrusted(hooks) {
			return errHookTrust
		}
		return nil
	})
}

func (trust hookTrust) Inspect(ctx context.Context) (bool, error) {
	var trusted bool
	err := trust.withServer(ctx, func(server *hookServer, path string) error {
		hooks, complete, err := trust.list(server, path)
		trusted = complete && allHooksTrusted(hooks)
		return err
	})
	return trusted, err
}

func (trust hookTrust) withServer(ctx context.Context, action func(*hookServer, string) error) error {
	path, err := filepath.Abs(trust.path)
	if err != nil {
		return fmt.Errorf("resolving Codex hook path: %w", err)
	}
	return runHookServer(ctx, filepath.Dir(path), func(server *hookServer) error {
		return action(server, path)
	})
}

func (trust hookTrust) list(server *hookServer, path string) ([]nativeHook, bool, error) {
	var result hooksList
	if err := server.request("hooks/list", map[string]any{"cwds": []string{filepath.Dir(path)}}, &result); err != nil {
		return nil, false, err
	}
	commands := make(map[string]bool, len(trust.hooks))
	for _, spec := range trust.hooks {
		commands[spec.Command] = false
	}
	var owned []nativeHook
	for _, entry := range result.Data {
		if len(entry.Errors) != 0 {
			return nil, false, fmt.Errorf("%w: %s: %s", errHookDiscovery, entry.Errors[0].Path, entry.Errors[0].Message)
		}
		for _, hook := range entry.Hooks {
			if _, expected := commands[hook.Command]; !expected || !hook.userCommandAt(path) {
				continue
			}
			if err := validateHookTrust(hook, path); err != nil {
				return nil, false, err
			}
			owned = append(owned, hook)
			commands[hook.Command] = true
		}
	}
	for _, found := range commands {
		if !found {
			return owned, false, nil
		}
	}
	return owned, true, nil
}

func (hook nativeHook) userCommandAt(path string) bool {
	return hook.SourcePath == path && hook.Source == "user" && hook.HandlerType == "command"
}

func validateHookTrust(hook nativeHook, path string) error {
	if !strings.HasPrefix(hook.Key, path+":") || !strings.HasPrefix(hook.CurrentHash, "sha256:") || len(hook.CurrentHash) <= len("sha256:") {
		return fmt.Errorf("%w: %s", errHookMetadata, hook.Key)
	}
	switch hook.TrustStatus {
	case "trusted", "modified", "untrusted":
		return nil
	default:
		return fmt.Errorf("%w: %s has trust status %q", errHookMetadata, hook.Key, hook.TrustStatus)
	}
}

func allHooksTrusted(hooks []nativeHook) bool {
	for _, hook := range hooks {
		if hook.TrustStatus != "trusted" {
			return false
		}
	}
	return true
}

func (codexHarness) InstallNextStep(_ bool, _ bool) string {
	return ""
}

func (codexHarness) StatusNextStep(_ bool, stale bool) string {
	if stale {
		return "run aht manage integrations install codex to update and automatically trust only aht's hooks"
	}
	return ""
}
