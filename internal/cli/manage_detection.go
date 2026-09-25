package cli

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/agentstate"
	harnesspkg "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/detection"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	detectionRuleWidth     = 24
	detectionStateWidth    = 12
	detectionPriorityWidth = 8
	detectionRegionWidth   = 10
	detectionMatchWidth    = 8
	detectionReasonWidth   = 44
)

var (
	errDetectionTestHarnessRequired    = errors.New("harness argument is required")
	errDetectionScreenRequired         = errors.New("--screen is required")
	errDetectionManifestConfigConflict = errors.New("--manifest and --config-dir cannot be used together")
)

type detectionTestOptions struct {
	screenPath   string
	manifestPath string
	configDir    string
	title        string
	showScreen   bool
}

func (app *application) newDetectionCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "detection",
		Short:         "Inspect and test agent state detection rules",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(app.newDetectionTestCommand())
	return command
}

func (app *application) newDetectionTestCommand() *cobra.Command {
	options := detectionTestOptions{}
	var harnessID registry.Harness
	command := &cobra.Command{
		Use:           "test <harness>",
		Short:         "Test detection rules against a saved screen fixture",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return exitCode(errDetectionTestHarnessRequired, exitCodeUsage)
			}
			return nil
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if options.screenPath == "" {
				return exitCode(errDetectionScreenRequired, exitCodeUsage)
			}
			if options.manifestPath != "" && options.configDir != "" {
				return exitCode(errDetectionManifestConfigConflict, exitCodeUsage)
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if options.manifestPath == "" && !cmd.Flags().Changed("config-dir") {
				options.configDir = cfg.Detection.ManifestsDir
			}
			harnessID, err = harnesspkg.Normalize(args[0])
			if err != nil {
				if options.manifestPath == "" {
					return exitCode(err, exitCodeUsage)
				}
				harnessID = registry.Harness(strings.TrimSpace(args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runDetectionTest(cmd.Context(), harnessID, options)
		},
	}

	command.Flags().StringVar(&options.screenPath, "screen", "", "saved terminal screen fixture `<path>` or - for stdin")
	command.Flags().StringVar(&options.manifestPath, "manifest", "", "explicit detection manifest `<path>`")
	command.Flags().StringVar(&options.configDir, "config-dir", "", "detection manifest override `<dir>`")
	command.Flags().StringVar(&options.title, "title", "", "terminal window or pane `<title>`")
	command.Flags().BoolVar(&options.showScreen, "show-screen", false, "echo screen text in output")

	return command
}

func (app *application) runDetectionTest(ctx context.Context, harnessID registry.Harness, opts detectionTestOptions) error {
	rawScreen, err := agentstate.ReadScreenInput(opts.screenPath, app.stdin)
	if err != nil {
		return exitCode(err, exitCodeGeneral)
	}

	inspection, err := detection.Inspect(ctx, harnessID, rawScreen, detection.Options{
		Title: opts.title, ManifestPath: opts.manifestPath, ConfigDir: opts.configDir, IncludeScreen: opts.showScreen,
	})
	if err != nil {
		return exitCode(err, exitCodeGeneral)
	}

	if app.outputJSON {
		return app.writeJSON(inspection)
	}

	return app.writeDetectionInspection(inspection)
}

func (app *application) writeDetectionInspection(inspection detection.Inspection) error {
	details := []humanDetail{
		{label: "Harness", value: string(inspection.Harness)},
		{label: "Manifest source", value: inspection.ManifestSource},
		{label: "Manifest version", value: strconv.Itoa(inspection.ManifestVersion)},
	}
	if inspection.Warning != "" {
		details = append(details, humanDetail{label: "Warning", value: inspection.Warning})
	}
	if inspection.Title != "" {
		details = append(details, humanDetail{label: "Title", value: inspection.Title})
	}
	details = append(details,
		humanDetail{label: "Lines evaluated", value: strconv.Itoa(inspection.LinesEvaluated)},
		humanDetail{label: "Effective activity", value: string(inspection.Decision.Activity)},
		humanDetail{label: "Reason", value: inspection.Decision.Reason},
	)
	winning := inspection.WinningRule
	if winning == "" {
		winning = "none"
	}
	details = append(details, humanDetail{label: "Winning rule", value: winning})

	if err := app.writeHumanDetails(details); err != nil {
		return err
	}

	if err := app.writeDetectionCandidatesTable(inspection.Candidates); err != nil {
		return err
	}

	if inspection.Screen != "" {
		if err := app.writeln(""); err != nil {
			return err
		}
		if err := app.writeln("Screen:"); err != nil {
			return err
		}
		if err := app.writeln(inspection.Screen); err != nil {
			return err
		}
	}

	return nil
}

func (app *application) writeDetectionCandidatesTable(candidates []detection.CandidateRule) error {
	if len(candidates) == 0 {
		return nil
	}
	if err := app.writeln(""); err != nil {
		return err
	}
	columns := []humanColumn{
		{heading: "Rule", width: detectionRuleWidth, wrap: wrapHumanIdentifier},
		{heading: "State", width: detectionStateWidth},
		{heading: "Priority", width: detectionPriorityWidth, align: text.AlignRight},
		{heading: "Region", width: detectionRegionWidth},
		{heading: "Match", width: detectionMatchWidth},
		{heading: "Reason", width: detectionReasonWidth},
	}
	rows := make([][]string, 0, len(candidates))
	for _, c := range candidates {
		matchStr := "no"
		if c.Winner {
			matchStr = "winner"
		} else if c.Matched {
			matchStr = "match"
		}
		region := c.Region
		if region == "" {
			region = "all"
		}
		rows = append(rows, []string{
			c.ID,
			c.State,
			strconv.Itoa(c.Priority),
			region,
			matchStr,
			c.Reason,
		})
	}
	return app.writeWrappedHumanTable(columns, rows)
}
