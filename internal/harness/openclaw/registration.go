package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/internal/harness"
)

const (
	inspectTimeout  = 30 * time.Second
	mutationTimeout = 30 * time.Second
	outputLimit     = 512 * 1024
)

var (
	errCLIRequired = errors.New("OpenClaw CLI is required to manage the plugin")
	errNixMode     = errors.New("OpenClaw plugin changes are disabled by OPENCLAW_NIX_MODE")
)

type registration struct {
	command                 string
	pluginID                string
	version                 string
	allowConversationAccess bool
}

type inspectReport struct {
	Plugin struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Source  string `json:"source"`
		Version string `json:"version"`
	} `json:"plugin"`
	Policy struct {
		AllowConversationAccess bool `json:"allowConversationAccess"` //nolint:tagliatelle // External OpenClaw API.
	} `json:"policy"`
	Install struct {
		Source      string `json:"source"`
		SourcePath  string `json:"sourcePath"`  //nolint:tagliatelle // External OpenClaw API.
		InstallPath string `json:"installPath"` //nolint:tagliatelle // External OpenClaw API.
		Version     string `json:"version"`
	} `json:"install"`
}

func newRegistration(command string, pluginID string, version string, allowConversationAccess bool) harness.PluginRegistration {
	return registration{command: command, pluginID: pluginID, version: version, allowConversationAccess: allowConversationAccess}
}

func (registration registration) ID() string {
	return registration.pluginID
}

func (registration registration) Label() string {
	return "OpenClaw plugin " + registration.pluginID
}

//nolint:cyclop // Classification checks independent native registration dimensions.
func (registration registration) Inspect(ctx context.Context, pluginDir string) (harness.PluginRegistrationState, error) {
	if _, err := exec.LookPath(registration.command); err != nil {
		if _, statErr := os.Stat(pluginDir); errors.Is(statErr, os.ErrNotExist) {
			return harness.PluginRegistrationMissing, nil
		}
		return harness.PluginRegistrationMissing, fmt.Errorf("%w: executable %q was not found", errCLIRequired, registration.command)
	}

	output, err := harness.RunCommand(ctx, inspectTimeout, outputLimit, registration.command, "plugins", "inspect", "--all", "--json")
	if err != nil {
		return harness.PluginRegistrationMissing, fmt.Errorf("inspecting OpenClaw plugins: %w", err)
	}
	reports, err := decodeInspectReports(output)
	if err != nil {
		return harness.PluginRegistrationMissing, err
	}
	for _, report := range reports {
		if report.Plugin.ID != registration.pluginID {
			continue
		}
		if !sameCleanPath(report.Install.SourcePath, pluginDir) {
			return harness.PluginRegistrationForeign, nil
		}
		version := report.Plugin.Version
		if version == "" {
			version = report.Install.Version
		}
		if report.Plugin.Status != "loaded" || report.Install.Source != "path" || version != registration.version ||
			(registration.allowConversationAccess && !report.Policy.AllowConversationAccess) {
			return harness.PluginRegistrationStale, nil
		}
		return harness.PluginRegistrationCurrent, nil
	}
	return harness.PluginRegistrationMissing, nil
}

func (registration registration) EnsureMutable(pluginDir string) error {
	if strings.TrimSpace(os.Getenv("OPENCLAW_NIX_MODE")) == "1" {
		return errNixMode
	}
	if _, err := exec.LookPath(registration.command); err != nil {
		return fmt.Errorf("%w: executable %q was not found", errCLIRequired, registration.command)
	}
	return nil
}

func (registration registration) Install(ctx context.Context, pluginDir string) error {
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "plugins", "install", pluginDir, "--link", "--force", "--accept-capabilities"); err != nil {
		return fmt.Errorf("registering OpenClaw plugin: %w", err)
	}
	if !registration.allowConversationAccess {
		return nil
	}
	key := "plugins.entries." + registration.pluginID + ".hooks.allowConversationAccess"
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "config", "set", key, "true", "--strict-json"); err != nil {
		return fmt.Errorf("granting OpenClaw conversation-hook access: %w", err)
	}
	return nil
}

func (registration registration) CleanupFailedInstall(ctx context.Context, previousState harness.PluginRegistrationState, pluginDir string) error {
	if previousState != harness.PluginRegistrationMissing {
		return nil
	}
	return registration.Remove(ctx, pluginDir)
}

func (registration registration) Remove(ctx context.Context, pluginDir string) error {
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "plugins", "uninstall", registration.pluginID, "--force"); err != nil {
		return fmt.Errorf("unregistering OpenClaw plugin: %w", err)
	}
	return nil
}

func decodeInspectReports(data []byte) ([]inspectReport, error) {
	reports := make([]inspectReport, 0)
	if err := json.Unmarshal(data, &reports); err == nil {
		return reports, nil
	}
	var wrapper struct {
		Plugins []inspectReport `json:"plugins"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing OpenClaw plugin inspection: %w", err)
	}
	return wrapper.Plugins, nil
}

func sameCleanPath(left string, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return filepath.Clean(left) == filepath.Clean(right)
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
