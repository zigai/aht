package hermes

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

	"github.com/zigai/aht/v2/internal/harness"
)

const (
	inspectTimeout  = 30 * time.Second
	mutationTimeout = 30 * time.Second
	outputLimit     = 64 * 1024
)

var (
	errCLIRequired = errors.New("hermes CLI is required to manage the plugin")
	errManagedMode = errors.New("hermes plugin changes are disabled for package-manager-managed installations")
)

type registration struct {
	command  string
	pluginID string
	version  string
}

type pluginReport struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

func newRegistration(command string, pluginID string, version string) harness.PluginRegistration {
	return registration{command: command, pluginID: pluginID, version: version}
}

func (registration registration) ID() string {
	return registration.pluginID
}

func (registration registration) Label() string {
	return "Hermes plugin " + registration.pluginID
}

func (registration registration) Inspect(ctx context.Context, pluginDir string) (harness.PluginRegistrationState, error) {
	if _, err := exec.LookPath(registration.command); err != nil {
		if _, statErr := os.Stat(pluginDir); errors.Is(statErr, os.ErrNotExist) {
			return harness.PluginRegistrationMissing, nil
		}
		return harness.PluginRegistrationMissing, fmt.Errorf("%w: executable %q was not found", errCLIRequired, registration.command)
	}

	output, err := harness.RunCommand(ctx, inspectTimeout, outputLimit, registration.command, "plugins", "list", "--user", "--json")
	if err != nil {
		return harness.PluginRegistrationMissing, fmt.Errorf("inspecting Hermes plugins: %w", err)
	}
	reports := make([]pluginReport, 0)
	if err := json.Unmarshal(output, &reports); err != nil {
		return harness.PluginRegistrationMissing, fmt.Errorf("parsing Hermes plugin inspection: %w", err)
	}
	for _, report := range reports {
		if report.Name != registration.pluginID {
			continue
		}
		if report.Status == "enabled" && report.Source == "user" && report.Version == registration.version {
			return harness.PluginRegistrationCurrent, nil
		}
		return harness.PluginRegistrationStale, nil
	}
	return harness.PluginRegistrationMissing, nil
}

func (registration registration) EnsureMutable(pluginDir string) error {
	if strings.TrimSpace(os.Getenv("HERMES_MANAGED")) != "" {
		return errManagedMode
	}
	managedMarker := filepath.Join(filepath.Dir(filepath.Dir(pluginDir)), ".managed")
	if _, err := os.Stat(managedMarker); err == nil {
		return errManagedMode
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking Hermes managed-mode marker: %w", err)
	}
	if _, err := exec.LookPath(registration.command); err != nil {
		return fmt.Errorf("%w: executable %q was not found", errCLIRequired, registration.command)
	}
	return nil
}

func (registration registration) Install(ctx context.Context, pluginDir string) error {
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "plugins", "enable", registration.pluginID, "--no-allow-tool-override"); err != nil {
		return fmt.Errorf("enabling Hermes plugin: %w", err)
	}
	return nil
}

func (registration registration) CleanupFailedInstall(ctx context.Context, previousState harness.PluginRegistrationState, pluginDir string) error {
	if previousState == harness.PluginRegistrationCurrent {
		return nil
	}
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "plugins", "disable", registration.pluginID); err != nil {
		return fmt.Errorf("disabling Hermes plugin: %w", err)
	}
	return nil
}

func (registration registration) Remove(ctx context.Context, pluginDir string) error {
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "plugins", "disable", registration.pluginID); err != nil {
		return fmt.Errorf("disabling Hermes plugin: %w", err)
	}
	if _, err := harness.RunCommand(ctx, mutationTimeout, outputLimit, registration.command, "plugins", "remove", registration.pluginID); err != nil {
		return fmt.Errorf("removing Hermes plugin: %w", err)
	}
	return nil
}
