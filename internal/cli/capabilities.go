package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

type capabilitiesOptions struct {
	harness string
}

func (app *application) newCapabilitiesCommand() *cobra.Command {
	options := capabilitiesOptions{harness: ""}
	command := &cobra.Command{
		Use:           "capabilities",
		Short:         "List supported agent harness capabilities and features",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.harness != "" {
				harnessID, err := harness.Parse(options.harness)
				if err != nil {
					return exitCode(fmt.Errorf("%w: unknown harness %q", registry.ErrUnknownHarness, options.harness), exitCodeUsage)
				}
				caps, ok := harness.CapabilitiesFor(harnessID)
				if !ok {
					return exitCode(fmt.Errorf("%w: unknown harness %q", registry.ErrUnknownHarness, options.harness), exitCodeUsage)
				}
				if app.outputJSON {
					return app.writeJSON(caps)
				}
				return app.writeSingleHarnessCapabilities(caps)
			}

			all := harness.AllCapabilities()
			if app.outputJSON {
				return app.writeJSON(all)
			}
			return app.writeCapabilitiesTable(all)
		},
	}
	command.Flags().StringVar(&options.harness, "harness", "", "filter capabilities to `<harness>`")
	return command
}

func (app *application) writeCapabilitiesTable(capabilities []harness.Capabilities) error {
	const (
		capabilityAgentWidth     = 12
		capabilityAuthorityWidth = 9
		capabilityEventWidth     = 5
		capabilityRunningWidth   = 8
		capabilityWaitingWidth   = 7
		capabilitySignalWidth    = 7
		capabilityFeatureWidth   = 7
	)
	rows := make([][]string, 0, len(capabilities))
	for _, c := range capabilities {
		rows = append(rows, []string{
			string(c.Harness),
			c.Authority,
			yesNo(c.SessionStart),
			yesNo(c.SessionEnd),
			yesNo(c.RunningIdle),
			yesNo(c.WaitingPermission),
			yesNo(c.ProcessIdentity),
			yesNo(c.NativeCatalog),
			yesNo(c.TTYTmuxContext),
			yesNo(c.Installable),
			yesNo(c.Resumable),
			yesNo(c.ScreenSupport),
		})
	}
	return app.writeHumanTable(
		[]humanColumn{
			{heading: "Harness", width: capabilityAgentWidth},
			{heading: "Authority", width: capabilityAuthorityWidth},
			{heading: "Start", width: capabilityEventWidth},
			{heading: "End", width: capabilityEventWidth},
			{heading: "Run/Idle", width: capabilityRunningWidth},
			{heading: "Wait", width: capabilityWaitingWidth},
			{heading: "Process", width: capabilitySignalWidth},
			{heading: "Catalog", width: capabilitySignalWidth},
			{heading: "TTY/MUX", width: capabilitySignalWidth},
			{heading: "Install", width: capabilityFeatureWidth},
			{heading: "Resume", width: capabilityFeatureWidth},
			{heading: "Screen", width: capabilityFeatureWidth},
		},
		rows,
	)
}

func (app *application) writeSingleHarnessCapabilities(c harness.Capabilities) error {
	return app.writeHumanDetails([]humanDetail{
		{label: "Harness", value: string(c.Harness)},
		{label: "Authority", value: c.Authority},
		{label: "Integration source", value: c.IntegrationSource},
		{label: "Integration version", value: strconv.Itoa(c.IntegrationVersion)},
		{label: "Session start", value: yesNo(c.SessionStart)},
		{label: "Session end", value: yesNo(c.SessionEnd)},
		{label: "Running/idle", value: yesNo(c.RunningIdle)},
		{label: "Waiting permission", value: yesNo(c.WaitingPermission)},
		{label: "Process identity", value: yesNo(c.ProcessIdentity)},
		{label: "Native catalog", value: yesNo(c.NativeCatalog)},
		{label: "TTY/MUX context", value: yesNo(c.TTYTmuxContext)},
		{label: "Installable", value: yesNo(c.Installable)},
		{label: "Resumable", value: yesNo(c.Resumable)},
		{label: "Screen support", value: yesNo(c.ScreenSupport)},
		{label: "Screen fallback", value: yesNo(c.ScreenFallback)},
	})
}
