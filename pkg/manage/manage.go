package manage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	// ErrForeignTracker means the service definition is not owned by AHT.
	ErrForeignTracker = errors.New("tracker service definition is not owned by aht")
	// ErrConfig means the AHT configuration could not be loaded.
	ErrConfig = errors.New("invalid aht configuration")
)

// Config identifies the AHT binary, registry, configuration, and tracker
// settings managed by a Manager. Empty fields use the current user's defaults.
// A bare Binary name (including the default "aht") is looked up on PATH when
// managing the tracker; a relative or absolute path is used as supplied.
//
//exhaustruct:optional
type Config struct {
	Binary    string
	StorePath string
	// ConfigPath selects the AHT config file, as --config does. "-" reads
	// ConfigStdin.
	ConfigPath  string
	ConfigStdin io.Reader
	// NoConfig ignores configuration files, as --no-config does.
	NoConfig bool
	// TrackerInterval overrides tracker.interval when positive.
	TrackerInterval time.Duration
	// TrackerGracePeriod overrides tracker.grace_period when set.
	TrackerGracePeriod *time.Duration
}

// Manager installs harness integrations and controls the platform-native AHT tracker.
type Manager struct {
	config      Config
	stdinConfig func() loadedConfig
}

// IntegrationOptions controls one managed harness integration operation.
//
//exhaustruct:optional
type IntegrationOptions struct {
	TargetBinary string
	DryRun       bool
	Force        bool
	UseShim      bool
}

// IntegrationResult describes the effect of installing or removing one integration.
// Error is set when the operation failed.
type IntegrationResult struct {
	Harness  registry.Harness `json:"harness"`
	Path     string           `json:"path"`
	Changed  bool             `json:"changed"`
	Message  string           `json:"message"`
	NextStep string           `json:"next_step,omitempty"`
	Snippet  string           `json:"snippet,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// TrackerOptions controls a tracker operation.
type TrackerOptions struct {
	DryRun bool
}

// TrackerResult describes the platform-native tracker state after an operation.
type TrackerResult struct {
	Platform       string `json:"platform"`
	Manager        string `json:"manager"`
	ManagedPath    string `json:"managed_path"`
	ManagedVersion int    `json:"managed_version"`
	Installed      bool   `json:"installed"`
	Current        bool   `json:"current"`
	Running        bool   `json:"running"`
	Changed        bool   `json:"changed"`
	Message        string `json:"message"`
}

type loadedConfig struct {
	cfg  config.Config
	meta *strata.Metadata
	path string
	err  error
}

// New returns a Manager with normalized local defaults.
func New(cfg Config) *Manager {
	if cfg.Binary == "" {
		cfg.Binary = "aht"
	}

	if cfg.StorePath == "" {
		cfg.StorePath = registry.DefaultStorePath()
	}

	m := &Manager{config: cfg, stdinConfig: nil}
	m.stdinConfig = sync.OnceValue(func() loadedConfig {
		cfg, meta, path, err := m.readConfigWithMetadata()
		return loadedConfig{cfg: cfg, meta: meta, path: path, err: err}
	})

	return m
}

// InstallableHarnesses returns the harnesses with managed integrations.
func InstallableHarnesses() []registry.Harness {
	return install.Harnesses()
}

// InstallIntegration idempotently installs or updates one harness integration.
func (m *Manager) InstallIntegration(
	ctx context.Context,
	harness registry.Harness,
	opts IntegrationOptions,
) (IntegrationResult, error) {
	result, err := install.Run(ctx, m.installOptions(harness, opts))

	converted := integrationResult(result)
	if err != nil {
		converted.Harness = harness
		converted.Error = err.Error()

		return converted,
			fmt.Errorf("installing %s integration: %w", harness, err)
	}

	return converted, nil
}

// RemoveIntegration removes only artifacts owned by AHT for one harness.
func (m *Manager) RemoveIntegration(
	ctx context.Context,
	harness registry.Harness,
	opts IntegrationOptions,
) (IntegrationResult, error) {
	result, err := install.Remove(ctx, m.installOptions(harness, opts))

	converted := integrationResult(result)
	if err != nil {
		converted.Harness = harness
		converted.Error = err.Error()

		return converted,
			fmt.Errorf("removing %s integration: %w", harness, err)
	}

	return converted, nil
}

// UpgradeIntegrations refreshes every installed integration, preserving native
// settings. Harnesses without an installed integration are skipped.
func (m *Manager) UpgradeIntegrations(
	ctx context.Context,
	opts IntegrationOptions,
) ([]IntegrationResult, error) {
	results, err := install.Upgrade(ctx, m.config.Binary, opts.DryRun)

	converted := make([]IntegrationResult, 0, len(results))
	for _, result := range results {
		converted = append(converted, integrationResult(result))
	}

	if err != nil {
		return converted, fmt.Errorf("upgrading integrations: %w", err)
	}

	return converted, nil
}

// IntegrationStatus reports whether one harness integration is installed and current.
func (m *Manager) IntegrationStatus(
	ctx context.Context,
	harness registry.Harness,
) (install.IntegrationStatus, error) {
	status, err := install.Inspect(ctx, harness, m.config.Binary)
	if err != nil {
		return install.IntegrationStatus{}, fmt.Errorf(
			"inspecting %s integration: %w",
			harness,
			err,
		)
	}

	return status, nil
}

// EnableTracker installs, updates, and starts background agent-session tracking.
func (m *Manager) EnableTracker(ctx context.Context, opts TrackerOptions) (TrackerResult, error) {
	options, err := m.serviceOptions(opts)
	if err != nil {
		return TrackerResult{}, fmt.Errorf("enabling tracker: %w", err)
	}

	result, err := service.Update(ctx, options)

	return trackerResult(result), trackerError("enabling tracker", err)
}

// DisableTracker stops and removes background agent-session tracking.
func (m *Manager) DisableTracker(ctx context.Context, opts TrackerOptions) (TrackerResult, error) {
	result, err := service.Uninstall(ctx, opts.DryRun)

	return trackerResult(result), trackerError("disabling tracker", err)
}

// TrackerStatus reports the state of background agent-session tracking.
func (m *Manager) TrackerStatus(ctx context.Context) (TrackerResult, error) {
	options, err := m.serviceOptions(TrackerOptions{DryRun: false})
	if err != nil {
		return TrackerResult{}, fmt.Errorf("checking tracker status: %w", err)
	}

	result, err := service.Status(ctx, options)

	return trackerResult(result), trackerError("checking tracker status", err)
}

// UpgradeTracker refreshes an installed tracker definition for the manager's
// binary, preserving its stored settings and running state. A tracker that is
// not installed stays uninstalled.
func (m *Manager) UpgradeTracker(ctx context.Context, opts TrackerOptions) (TrackerResult, error) {
	result, err := service.Upgrade(ctx, m.config.Binary, opts.DryRun)

	return trackerResult(result), trackerError("upgrading tracker", err)
}

func (m *Manager) ValidateConfig() error {
	_, err := m.loadConfig()
	return err
}

func (m *Manager) installOptions(
	harness registry.Harness,
	opts IntegrationOptions,
) install.Options {
	return install.Options{
		Harness:      harness,
		Binary:       m.config.Binary,
		TargetBinary: opts.TargetBinary,
		DryRun:       opts.DryRun,
		Force:        opts.Force,
		UseShim:      opts.UseShim,
	}
}

func (m *Manager) serviceOptions(opts TrackerOptions) (service.Options, error) {
	cfg, err := m.loadConfig()
	if err != nil {
		return service.Options{}, err
	}

	interval, grace, err := config.TrackerDurations(cfg)
	if err != nil {
		return service.Options{}, fmt.Errorf("resolving tracker durations: %w", err)
	}

	if m.config.TrackerInterval > 0 {
		interval = m.config.TrackerInterval
	}

	if m.config.TrackerGracePeriod != nil {
		grace = *m.config.TrackerGracePeriod
	}

	return service.Options{
		Binary:      m.config.Binary,
		StorePath:   m.config.StorePath,
		Interval:    interval,
		GracePeriod: grace,
		DryRun:      opts.DryRun,
	}, nil
}

func (m *Manager) loadConfig() (config.Config, error) {
	cfg, _, _, err := m.loadConfigWithMetadata()
	return cfg, err
}

func (m *Manager) loadConfigWithMetadata() (config.Config, *strata.Metadata, string, error) {
	path := m.config.ConfigPath
	if path == "" {
		path = strings.TrimSpace(os.Getenv(config.ConfigEnv))
	}

	if path == "-" && !m.config.NoConfig {
		loaded := m.stdinConfig()
		return loaded.cfg, loaded.meta, loaded.path, loaded.err
	}

	return m.readConfigWithMetadata()
}

func (m *Manager) readConfigWithMetadata() (config.Config, *strata.Metadata, string, error) {
	cfg, meta, resolved, err := config.LoadWithMetadata(config.Options{
		Path:     m.config.ConfigPath,
		Explicit: m.config.ConfigPath != "",
		NoConfig: m.config.NoConfig,
		Stdin:    m.config.ConfigStdin,
	})
	if err != nil {
		return config.Config{}, nil, resolved, fmt.Errorf("%w: %w", ErrConfig, err)
	}

	return cfg, meta, resolved, nil
}

func integrationResult(result install.Result) IntegrationResult {
	return IntegrationResult{
		Harness:  registry.Harness(result.Harness),
		Path:     result.Path,
		Changed:  result.Changed,
		Message:  result.Message,
		NextStep: result.NextStep,
		Snippet:  result.Snippet,
		Error:    result.Error,
	}
}

func trackerResult(result service.Result) TrackerResult {
	return TrackerResult{
		Platform:       result.Platform,
		Manager:        result.Manager,
		ManagedPath:    result.ManagedPath,
		ManagedVersion: result.ManagedVersion,
		Installed:      result.Installed,
		Current:        result.Current,
		Running:        result.Running,
		Changed:        result.Changed,
		Message:        result.Message,
	}
}

func trackerError(operation string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, service.ErrForeign) {
		return fmt.Errorf("%s: %w: %w", operation, ErrForeignTracker, err)
	}

	return fmt.Errorf("%s: %w", operation, err)
}
