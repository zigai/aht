package manage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultTrackerInterval = 300 * time.Millisecond

	ArtifactMissing ArtifactStatus = "missing"
	ArtifactCurrent ArtifactStatus = "current"
	ArtifactStale   ArtifactStatus = "stale"
	ArtifactForeign ArtifactStatus = "foreign"
)

var (
	// ErrForeignTracker means the service definition is not owned by AHT.
	ErrForeignTracker = errors.New("tracker service definition is not owned by aht")
	// ErrUnsupportedTracker means background tracking is unavailable on this platform.
	ErrUnsupportedTracker = errors.New("background tracking is unsupported")
)

// Config identifies the AHT binary, registry, and tracker settings managed by a Manager.
// Empty fields use the current user's defaults.
// A bare Binary name (including the default "aht") is looked up on PATH when
// managing the tracker; a relative or absolute path is used as supplied.
type Config struct {
	Binary             string
	StorePath          string
	TrackerInterval    time.Duration
	TrackerGracePeriod time.Duration
}

// Manager installs harness integrations and controls the platform-native AHT tracker.
type Manager struct {
	config Config
}

// IntegrationOptions controls one managed harness integration operation.
type IntegrationOptions struct {
	TargetBinary string
	DryRun       bool
	Force        bool
	UseShim      bool
}

// IntegrationResult describes the effect of installing or removing one integration.
type IntegrationResult struct {
	Harness  registry.Harness
	Path     string
	Changed  bool
	Message  string
	NextStep string
	Snippet  string
}

// ArtifactStatus describes the ownership and freshness of a managed integration artifact.
type ArtifactStatus string

// IntegrationStatus describes the installed state of one harness integration.
type IntegrationStatus struct {
	Harness  registry.Harness
	Status   ArtifactStatus
	Paths    []string
	Message  string
	NextStep string
}

// TrackerOptions controls a tracker operation.
type TrackerOptions struct {
	DryRun bool
}

// TrackerResult describes the platform-native tracker state after an operation.
type TrackerResult struct {
	Platform       string
	Manager        string
	ManagedPath    string
	ManagedVersion int
	Installed      bool
	Current        bool
	Running        bool
	Changed        bool
	Message        string
}

// New returns a Manager with normalized local defaults.
func New(config Config) *Manager {
	if config.Binary == "" {
		config.Binary = "aht"
	}
	if config.StorePath == "" {
		config.StorePath = registry.DefaultStorePath()
	}
	if config.TrackerInterval <= 0 {
		config.TrackerInterval = defaultTrackerInterval
	}
	return &Manager{config: config}
}

func (s ArtifactStatus) IsValid() bool {
	switch s {
	case ArtifactMissing, ArtifactCurrent, ArtifactStale, ArtifactForeign:
		return true
	}
	return false
}

// SupportedHarnesses returns the harnesses with managed integrations.
func SupportedHarnesses() []registry.Harness {
	return install.AllHarnesses()
}

// InstallIntegration idempotently installs or updates one harness integration.
func (m *Manager) InstallIntegration(
	ctx context.Context,
	harness registry.Harness,
	options IntegrationOptions,
) (IntegrationResult, error) {
	result, err := install.RunContext(ctx, install.Options{
		Harness:      harness,
		Binary:       m.config.Binary,
		TargetBinary: options.TargetBinary,
		DryRun:       options.DryRun,
		Force:        options.Force,
		UseShim:      options.UseShim,
	})
	if err != nil {
		return integrationResult(result), fmt.Errorf("installing %s integration: %w", harness, err)
	}
	return integrationResult(result), nil
}

// RemoveIntegration removes only artifacts owned by AHT for one harness.
func (m *Manager) RemoveIntegration(
	ctx context.Context,
	harness registry.Harness,
	options IntegrationOptions,
) (IntegrationResult, error) {
	result, err := install.RemoveContext(ctx, install.Options{
		Harness:      harness,
		Binary:       m.config.Binary,
		TargetBinary: options.TargetBinary,
		DryRun:       options.DryRun,
		Force:        options.Force,
		UseShim:      options.UseShim,
	})
	if err != nil {
		return integrationResult(result), fmt.Errorf("removing %s integration: %w", harness, err)
	}
	return integrationResult(result), nil
}

// IntegrationStatus reports whether one harness integration is installed and current.
func (m *Manager) IntegrationStatus(
	ctx context.Context,
	harness registry.Harness,
) (IntegrationStatus, error) {
	status, err := install.InspectContext(ctx, harness, m.config.Binary)
	if err != nil {
		return IntegrationStatus{}, fmt.Errorf("inspecting %s integration: %w", harness, err)
	}
	return IntegrationStatus{
		Harness:  status.Harness,
		Status:   ArtifactStatus(status.Status),
		Paths:    append([]string(nil), status.Paths...),
		Message:  status.Message,
		NextStep: status.NextStep,
	}, nil
}

// EnableTracker installs, updates, and starts background agent-session tracking.
func (m *Manager) EnableTracker(ctx context.Context, options TrackerOptions) (TrackerResult, error) {
	result, err := service.Update(ctx, m.serviceOptions(options))
	return trackerResult(result), trackerError("enabling tracker", err)
}

// DisableTracker stops and removes background agent-session tracking.
func (m *Manager) DisableTracker(ctx context.Context, options TrackerOptions) (TrackerResult, error) {
	result, err := service.Uninstall(ctx, m.serviceOptions(options))
	return trackerResult(result), trackerError("disabling tracker", err)
}

// TrackerStatus reports the state of background agent-session tracking.
func (m *Manager) TrackerStatus(ctx context.Context) (TrackerResult, error) {
	result, err := service.Status(ctx, m.serviceOptions(TrackerOptions{DryRun: false}))
	return trackerResult(result), trackerError("checking tracker status", err)
}

func (m *Manager) serviceOptions(options TrackerOptions) service.Options {
	return service.Options{
		Binary:      m.config.Binary,
		StorePath:   m.config.StorePath,
		Interval:    m.config.TrackerInterval,
		GracePeriod: m.config.TrackerGracePeriod,
		DryRun:      options.DryRun,
	}
}

func integrationResult(result install.Result) IntegrationResult {
	return IntegrationResult{
		Harness:  registry.Harness(result.Harness),
		Path:     result.Path,
		Changed:  result.Changed,
		Message:  result.Message,
		NextStep: result.NextStep,
		Snippet:  result.Snippet,
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
	if errors.Is(err, service.ErrUnsupported) {
		return fmt.Errorf("%s: %w: %w", operation, ErrUnsupportedTracker, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
