package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/internal/config"
	harness "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/registry"
)

const (
	doctorOK      doctorStatus = "ok"
	doctorWarning doctorStatus = "warning"
	doctorError   doctorStatus = "error"

	doctorCheckCapacity    = 10
	serviceDefaultInterval = 300 * time.Millisecond
)

type doctorStatus string

type doctorCheck struct {
	Name    string       `json:"name"`
	Status  doctorStatus `json:"status"`
	Message string       `json:"message"`
}

type doctorCapability struct {
	Harness         string `json:"harness"`
	SessionStart    bool   `json:"session_start"`
	SessionEnd      bool   `json:"session_end"`
	RunningIdle     bool   `json:"running_idle"`
	Waiting         bool   `json:"waiting_permission"`
	ProcessIdentity bool   `json:"process_identity"`
	NativeCatalog   bool   `json:"native_catalog"`
	TTYTmuxContext  bool   `json:"tty_tmux_context"`
}

type doctorResult struct {
	OK           bool               `json:"ok"`
	Checks       []doctorCheck      `json:"checks"`
	Capabilities []doctorCapability `json:"capabilities"`
}

type observerHealth struct {
	LastSuccessAt        time.Time `json:"last_success_at"`
	LastEnumerationError string    `json:"last_enumeration_error"`
}

func (app *application) newDoctorCommand() *cobra.Command {
	var verbose bool
	command := &cobra.Command{
		Use:           "doctor",
		Short:         "Check whether aht is set up and working",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result := app.runDoctor(cmd.Context(), verbose)
			if err := app.writeDoctorResult(result); err != nil {
				return err
			}
			if !result.OK {
				return exitCode(errDoctorFailed, exitCodeGeneral)
			}
			return nil
		},
	}
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "include all integrations and capability details")
	return command
}

func (app *application) writeDoctorResult(result doctorResult) error {
	const (
		doctorCheckNameWidth    = 32
		doctorCheckStatusWidth  = 9
		doctorCheckMessageWidth = 75
	)
	if app.outputJSON {
		return app.writeJSON(result)
	}
	rows := make([][]string, 0, len(result.Checks))
	for _, check := range result.Checks {
		rows = append(rows, []string{check.Name, string(check.Status), check.Message})
	}
	if err := app.writeWrappedHumanTable(
		[]humanColumn{{heading: "Check", width: doctorCheckNameWidth}, {heading: "Status", width: doctorCheckStatusWidth}, {heading: "Message", width: doctorCheckMessageWidth}},
		rows,
	); err != nil {
		return err
	}
	return app.writeDoctorCapabilities(result.Capabilities)
}

func (app *application) writeDoctorCapabilities(capabilities []doctorCapability) error {
	const (
		doctorCapabilityAgentWidth   = 12
		doctorCapabilityEventWidth   = 5
		doctorCapabilityRunningWidth = 8
		doctorCapabilityWaitingWidth = 7
		doctorCapabilitySignalWidth  = 7
	)
	if len(capabilities) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(capabilities))
	for _, capability := range capabilities {
		rows = append(rows, []string{capability.Harness, yesNo(capability.SessionStart), yesNo(capability.SessionEnd), yesNo(capability.RunningIdle), yesNo(capability.Waiting), yesNo(capability.ProcessIdentity), yesNo(capability.NativeCatalog), yesNo(capability.TTYTmuxContext)})
	}
	return app.writeHumanTable(
		[]humanColumn{{heading: "Agent", width: doctorCapabilityAgentWidth}, {heading: "Start", width: doctorCapabilityEventWidth}, {heading: "End", width: doctorCapabilityEventWidth}, {heading: "Run/Idle", width: doctorCapabilityRunningWidth}, {heading: "Wait", width: doctorCapabilityWaitingWidth}, {heading: "Process", width: doctorCapabilitySignalWidth}, {heading: "Catalog", width: doctorCapabilitySignalWidth}, {heading: "TTY/MUX", width: doctorCapabilitySignalWidth}},
		rows,
	)
}

//nolint:gocognit,gocritic,nestif,cyclop // the doctor command intentionally reports independent checks in one ordered result
func (app *application) runDoctor(ctx context.Context, includeAll bool) doctorResult {
	result := doctorResult{Checks: make([]doctorCheck, 0, doctorCheckCapacity+len(harness.All())), Capabilities: make([]doctorCapability, 0, len(harness.All()))}
	add := func(name string, status doctorStatus, message string) {
		result.Checks = append(result.Checks, doctorCheck{Name: name, Status: status, Message: message})
	}

	store := app.store()
	if _, err := store.List(ctx, registry.Filter{}); err != nil {
		if unsupported, ok := errors.AsType[*registry.UnsupportedSchemaError](err); ok {
			add("store.schema", doctorError, unsupported.Error())
		} else {
			add("store.schema", doctorError, err.Error())
		}
	} else {
		add("store.schema", doctorOK, "schema_version=2")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		add("observer.platform", doctorError, "unsupported platform: "+runtime.GOOS)
	} else {
		add("observer.platform", doctorOK, runtime.GOOS)
	}
	if _, err := processinfo.List(ctx); err != nil {
		if unsupported, ok := errors.AsType[*processinfo.UnsupportedError](err); ok {
			add("observer.process-enumeration", doctorError, unsupported.Error())
		} else {
			add("observer.process-enumeration", doctorError, err.Error())
		}
	} else {
		add("observer.process-enumeration", doctorOK, "complete current-user process inventory available")
	}
	serviceResult, serviceErr := app.doctorServiceStatus(ctx)
	if serviceErr != nil {
		if errors.Is(serviceErr, service.ErrUnsupported) {
			add("observer.service", doctorWarning, serviceErr.Error())
		} else {
			add("observer.service", doctorError, serviceErr.Error())
		}
	} else if !serviceResult.Installed {
		add("observer.service", doctorWarning, "managed tracker service is not installed")
	} else if !serviceResult.Current {
		add("observer.service", doctorWarning, "managed tracker service is stale; run aht manage tracker enable")
	} else if !serviceResult.Running {
		add("observer.service", doctorWarning, "managed tracker service is stopped")
	} else {
		add("observer.service", doctorOK, "managed tracker service is running")
	}
	result.addObserverReconciliationCheck(store.Path())
	result.addDetectionManifestCheck()
	app.addConfigFileCheck(&result)

	for _, adapter := range harness.All() {
		definition := adapter.Definition()
		if includeAll {
			result.Capabilities = append(result.Capabilities, doctorCapability{Harness: string(definition.ID), SessionStart: definition.Capabilities.SessionStart, SessionEnd: definition.Capabilities.SessionEnd, RunningIdle: definition.Capabilities.RunningIdle, Waiting: definition.Capabilities.WaitingPermission, ProcessIdentity: definition.Capabilities.ProcessIdentity, NativeCatalog: definition.Capabilities.NativeCatalog, TTYTmuxContext: definition.Capabilities.TTYTmuxContext})
		}
		status, message, relevant := integrationStatus(ctx, definition.ID)
		if includeAll || relevant {
			add("integration."+string(definition.ID), status, message)
		}
	}
	result.OK = true
	for _, check := range result.Checks {
		if check.Status == doctorError {
			result.OK = false
			break
		}
	}
	return result
}

func (app *application) doctorServiceStatus(ctx context.Context) (service.Result, error) {
	options, err := app.configuredServiceOptions(&cobra.Command{}, serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval})
	if err != nil {
		return service.Result{}, err
	}
	result, err := service.Status(ctx, options)
	if err != nil {
		return result, fmt.Errorf("tracker status: %w", err)
	}
	return result, nil
}

func (result *doctorResult) addObserverReconciliationCheck(storePath string) {
	path := storePath + ".observer-health.json"
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.Checks = append(result.Checks, doctorCheck{Name: "observer.reconciliation", Status: doctorWarning, Message: "tracker health is missing; run aht manage tracker run --once or aht manage tracker enable"})
			return
		}
		result.Checks = append(result.Checks, doctorCheck{Name: "observer.reconciliation", Status: doctorError, Message: err.Error()})
		return
	}
	var health observerHealth
	if err := json.Unmarshal(data, &health); err != nil {
		result.Checks = append(result.Checks, doctorCheck{Name: "observer.reconciliation", Status: doctorError, Message: "invalid observer health sidecar: " + err.Error()})
		return
	}
	if health.LastEnumerationError != "" {
		result.Checks = append(result.Checks, doctorCheck{Name: "observer.reconciliation", Status: doctorError, Message: health.LastEnumerationError})
		return
	}
	if health.LastSuccessAt.IsZero() {
		result.Checks = append(result.Checks, doctorCheck{Name: "observer.reconciliation", Status: doctorWarning, Message: "observer has not completed a successful reconciliation"})
		return
	}
	result.Checks = append(result.Checks, doctorCheck{Name: "observer.reconciliation", Status: doctorOK, Message: "last successful reconciliation at " + health.LastSuccessAt.Format(time.RFC3339)})
}

func (result *doctorResult) addDetectionManifestCheck() {
	loader := agentstate.Loader{}
	harnesses := registry.AllHarnesses()
	var warnings []string
	for _, harnessID := range harnesses {
		if !loader.Supports(harnessID) {
			continue
		}
		manifest, err := loader.Load(harnessID)
		if err != nil {
			result.Checks = append(result.Checks, doctorCheck{Name: "detection.manifests", Status: doctorError, Message: err.Error()})
			return
		}
		if manifest.Warning != "" {
			warnings = append(warnings, manifest.Warning)
		}
	}
	if len(warnings) > 0 {
		result.Checks = append(result.Checks, doctorCheck{Name: "detection.manifests", Status: doctorWarning, Message: strings.Join(warnings, "; ")})
		return
	}
	result.Checks = append(result.Checks, doctorCheck{Name: "detection.manifests", Status: doctorOK, Message: "all bundled and configured manifests are valid"})
}

func (app *application) addConfigFileCheck(result *doctorResult) {
	_, err := app.loadConfig()
	path := app.resolvedConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	if err != nil {
		result.Checks = append(result.Checks, doctorCheck{Name: "config.file", Status: doctorError, Message: err.Error()})
		return
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			result.Checks = append(result.Checks, doctorCheck{Name: "config.file", Status: doctorOK, Message: "no config file present (using defaults)"})
			return
		}
		result.Checks = append(result.Checks, doctorCheck{Name: "config.file", Status: doctorError, Message: statErr.Error()})
		return
	}
	result.Checks = append(result.Checks, doctorCheck{Name: "config.file", Status: doctorOK, Message: fmt.Sprintf("config file is valid (%s)", path)})
}

func integrationStatus(ctx context.Context, id registry.Harness) (doctorStatus, string, bool) {
	status, err := install.InspectContext(ctx, id, defaultInstallBinary())
	if err != nil {
		return doctorError, err.Error(), true
	}
	switch status.Status {
	case install.ArtifactCurrent:
		return doctorOK, "managed integration is current", true
	case install.ArtifactMissing:
		return doctorWarning, status.Message, false
	case install.ArtifactStale:
		return doctorWarning, status.Message, true
	case install.ArtifactForeign:
		return doctorError, status.Message, true
	default:
		return doctorWarning, status.Message, true
	}
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
