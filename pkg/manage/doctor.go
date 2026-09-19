package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/internal/config"
	"github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	// DoctorStatusOK indicates that the check passed normally.
	DoctorStatusOK DoctorStatus = "ok"

	// DoctorStatusWarning indicates a non-critical issue or uninstalled component.
	DoctorStatusWarning DoctorStatus = "warning"

	// DoctorStatusError indicates an error that prevents normal operation.
	DoctorStatusError DoctorStatus = "error"
)

// DoctorStatus represents the severity level of a doctor diagnostic check.
type (
	DoctorStatus string
	// DoctorCheck represents one diagnostic check evaluated by Doctor.
	DoctorCheck struct {
		Name    string       `json:"name"`
		Status  DoctorStatus `json:"status"`
		Message string       `json:"message"`
	}
)

// DoctorResult represents the complete diagnostic status of the AHT installation.
type DoctorResult struct {
	OK           bool                   `json:"ok"`
	Checks       []DoctorCheck          `json:"checks"`
	Capabilities []harness.Capabilities `json:"capabilities"`
}

// DoctorOptions controls what checks and details Doctor evaluates.
type DoctorOptions struct {
	IncludeAll   bool          // include uninstalled harness details and capabilities (verbose)
	ConfigPath   string        // optional config file path override
	MaxHealthAge time.Duration // maximum age before tracker health is considered stale
}

// Doctor performs a comprehensive diagnostic evaluation of the AHT installation,
// storage, background tracker, process enumeration, detection manifests, configuration,
// and harness integrations.
func (m *Manager) Doctor(ctx context.Context, options DoctorOptions) DoctorResult {
	const initialCheckCapacity = 16
	result := DoctorResult{
		OK:           true,
		Checks:       make([]DoctorCheck, 0, initialCheckCapacity),
		Capabilities: make([]harness.Capabilities, 0),
	}

	add := func(name string, status DoctorStatus, message string) {
		result.Checks = append(result.Checks, DoctorCheck{
			Name:    name,
			Status:  status,
			Message: message,
		})
	}
	m.checkStoreSchema(ctx, add)
	m.checkPlatform(add)
	m.checkProcessEnumeration(ctx, add)
	m.checkService(ctx, add)
	m.checkReconciliation(options.MaxHealthAge, add)
	m.CheckManifests(add)
	m.checkConfigFile(options.ConfigPath, add)
	m.checkIntegrations(ctx, options.IncludeAll, &result, add)

	result.OK = true
	for _, check := range result.Checks {
		if check.Status == DoctorStatusError {
			result.OK = false
			break
		}
	}

	return result
}

func (m *Manager) checkStoreSchema(ctx context.Context, add func(string, DoctorStatus, string)) {
	storePath := m.config.StorePath
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}
	store := registry.NewFileStore(storePath)
	if _, err := store.List(ctx, registry.Filter{Harness: "", Presence: "", Activity: "", TmuxSession: "", MultiplexerSession: "", Project: "", ProjectSubtree: false, CWD: "", MultiplexerKind: "", MultiplexerServer: "", MultiplexerPane: ""}); err != nil {
		if unsupported, ok := errors.AsType[*registry.UnsupportedSchemaError](err); ok {
			add("store.schema", DoctorStatusError, unsupported.Error())
		} else {
			add("store.schema", DoctorStatusError, err.Error())
		}
	} else {
		add("store.schema", DoctorStatusOK, "schema_version=2")
	}
}

func (m *Manager) checkPlatform(add func(string, DoctorStatus, string)) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		add("observer.platform", DoctorStatusError, "unsupported platform: "+runtime.GOOS)
	} else {
		add("observer.platform", DoctorStatusOK, runtime.GOOS)
	}
}

func (m *Manager) checkProcessEnumeration(ctx context.Context, add func(string, DoctorStatus, string)) {
	if _, err := processinfo.List(ctx); err != nil {
		if unsupported, ok := errors.AsType[*processinfo.UnsupportedError](err); ok {
			add("observer.process-enumeration", DoctorStatusError, unsupported.Error())
		} else {
			add("observer.process-enumeration", DoctorStatusError, err.Error())
		}
	} else {
		add("observer.process-enumeration", DoctorStatusOK, "complete current-user process inventory available")
	}
}

func (m *Manager) checkService(ctx context.Context, add func(string, DoctorStatus, string)) {
	serviceResult, serviceErr := m.TrackerStatus(ctx)
	switch {
	case serviceErr != nil:
		if errors.Is(serviceErr, service.ErrUnsupported) {
			add("observer.service", DoctorStatusWarning, serviceErr.Error())
		} else {
			add("observer.service", DoctorStatusError, serviceErr.Error())
		}
	case !serviceResult.Installed:
		add("observer.service", DoctorStatusWarning, "managed tracker service is not installed")
	case !serviceResult.Current:
		add("observer.service", DoctorStatusWarning, "managed tracker service is stale; run aht manage tracker enable")
	case !serviceResult.Running:
		add("observer.service", DoctorStatusWarning, "managed tracker service is stopped")
	default:
		add("observer.service", DoctorStatusOK, "managed tracker service is running")
	}
}

func (m *Manager) checkReconciliation(maxAge time.Duration, add func(string, DoctorStatus, string)) {
	health, err := ReadTrackerHealth(m.HealthPath(), time.Now().UTC(), maxAge)
	if err != nil {
		switch {
		case errors.Is(err, ErrHealthMissing):
			add("observer.reconciliation", DoctorStatusWarning, "tracker health is missing; run aht manage tracker run --once or aht manage tracker enable")
		case errors.Is(err, ErrHealthStale), errors.Is(err, ErrReconciliationIncomplete):
			add("observer.reconciliation", DoctorStatusWarning, health.Message)
		default:
			add("observer.reconciliation", DoctorStatusError, health.Message)
		}
		return
	}
	add("observer.reconciliation", DoctorStatusOK, health.Message)
}

// CheckManifests evaluates bundled and configured detection manifests.
func (m *Manager) CheckManifests(add func(string, DoctorStatus, string)) {
	loader := agentstate.Loader{ConfigDir: ""}
	harnesses := registry.AllHarnesses()
	var warnings []string
	for _, harnessID := range harnesses {
		if !loader.Supports(harnessID) {
			continue
		}
		manifest, err := loader.Load(harnessID)
		if err != nil {
			add("detection.manifests", DoctorStatusError, err.Error())
			return
		}
		if manifest.Warning != "" {
			warnings = append(warnings, manifest.Warning)
		}
	}
	if len(warnings) > 0 {
		add("detection.manifests", DoctorStatusWarning, strings.Join(warnings, "; "))
		return
	}
	add("detection.manifests", DoctorStatusOK, "all bundled and configured manifests are valid")
}

func (m *Manager) checkConfigFile(configPath string, add func(string, DoctorStatus, string)) {
	path := configPath
	if path == "" {
		path = config.DefaultPath()
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			add("config.file", DoctorStatusOK, "no config file present (using defaults)")
			return
		}
		add("config.file", DoctorStatusError, statErr.Error())
		return
	}
	if _, _, err := config.Load(path); err != nil {
		add("config.file", DoctorStatusError, err.Error())
		return
	}
	add("config.file", DoctorStatusOK, fmt.Sprintf("config file is valid (%s)", path))
}

func (m *Manager) checkIntegrations(
	ctx context.Context,
	includeAll bool,
	result *DoctorResult,
	add func(string, DoctorStatus, string),
) {
	for _, adapter := range catalog.All() {
		def := adapter.Definition()
		if includeAll {
			if caps, ok := harness.CapabilitiesFor(def.ID); ok {
				result.Capabilities = append(result.Capabilities, caps)
			}
		}
		status, message, relevant := m.integrationStatus(ctx, def.ID)
		if includeAll || relevant {
			add("integration."+string(def.ID), status, message)
		}
	}
}

func (m *Manager) integrationStatus(ctx context.Context, id registry.Harness) (DoctorStatus, string, bool) {
	st, err := m.IntegrationStatus(ctx, id)
	if err != nil {
		return DoctorStatusError, err.Error(), true
	}
	switch st.Status {
	case ArtifactCurrent:
		return DoctorStatusOK, "managed integration is current", true
	case ArtifactMissing:
		return DoctorStatusWarning, st.Message, false
	case ArtifactStale:
		return DoctorStatusWarning, st.Message, true
	case ArtifactForeign:
		return DoctorStatusError, st.Message, true
	default:
		return DoctorStatusWarning, st.Message, true
	}
}
