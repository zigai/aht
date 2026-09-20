package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/manage"
)

const (
	doctorOK      = manage.DoctorStatusOK
	doctorWarning = manage.DoctorStatusWarning
	doctorError   = manage.DoctorStatusError

	serviceDefaultInterval = 300 * time.Millisecond
)

type (
	doctorStatus     = manage.DoctorStatus
	doctorCheck      = manage.DoctorCheck
	doctorCapability = harness.Capabilities
)

type doctorResult struct {
	OK           bool               `json:"ok"`
	Checks       []doctorCheck      `json:"checks"`
	Capabilities []doctorCapability `json:"capabilities"`
}

func (app *application) newDoctorCommand() *cobra.Command {
	var verbose bool
	var manager *manage.Manager
	var result doctorResult
	command := &cobra.Command{
		Use:           "doctor",
		Short:         "Check whether aht is set up and working",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRun: func(cmd *cobra.Command, _ []string) {
			opts, err := app.configuredServiceOptions(cmd, serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval})
			if err != nil {
				// Configuration failures are diagnostic results, so doctor keeps
				// its structured output and ordinary failure exit code.
				manager = nil
				result = doctorResult{OK: false, Checks: []doctorCheck{{Name: "tracker configuration", Status: doctorError, Message: err.Error()}}, Capabilities: nil}
				return
			}
			manager = manage.New(manage.Config{Binary: opts.Binary, StorePath: opts.StorePath, TrackerInterval: opts.Interval, TrackerGracePeriod: opts.GracePeriod})
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if manager != nil {
				res := manager.Doctor(cmd.Context(), manage.DoctorOptions{IncludeAll: verbose, ConfigPath: app.resolvedConfigPath, MaxHealthAge: 0})
				result = doctorResult{OK: res.OK, Checks: res.Checks, Capabilities: res.Capabilities}
			}
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
		rows = append(rows, []string{string(capability.Harness), yesNo(capability.SessionStart), yesNo(capability.SessionEnd), yesNo(capability.RunningIdle), yesNo(capability.WaitingPermission), yesNo(capability.ProcessIdentity), yesNo(capability.NativeCatalog), yesNo(capability.TTYTmuxContext)})
	}
	return app.writeHumanTable(
		[]humanColumn{{heading: "Agent", width: doctorCapabilityAgentWidth}, {heading: "Start", width: doctorCapabilityEventWidth}, {heading: "End", width: doctorCapabilityEventWidth}, {heading: "Run/Idle", width: doctorCapabilityRunningWidth}, {heading: "Wait", width: doctorCapabilityWaitingWidth}, {heading: "Process", width: doctorCapabilitySignalWidth}, {heading: "Catalog", width: doctorCapabilitySignalWidth}, {heading: "TTY/MUX", width: doctorCapabilitySignalWidth}},
		rows,
	)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
