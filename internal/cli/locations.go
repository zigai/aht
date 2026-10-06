package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

type locationsOptions struct {
	projectDir string
}

func (app *application) newLocationsCommand() *cobra.Command {
	options := locationsOptions{projectDir: ""}
	command := &cobra.Command{
		Use:           "locations [harness...]",
		Short:         "List where agent harnesses keep settings, instructions, and skills",
		Long:          "List where agent harnesses read settings, instructions, skills, commands, and MCP definitions. Harnesses read some kinds from several alternative paths, so most paths do not exist. Without harness arguments, every harness is listed.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, args []string) error {
			projectDir, err := resolveProjectDir(options.projectDir)
			if err != nil {
				return err
			}
			harnesses, err := selectedLocationHarnesses(args)
			if err != nil {
				return err
			}
			locations := make([]harness.Location, 0, len(harnesses))
			for _, harnessID := range harnesses {
				found, _ := harness.LocationsFor(harnessID, projectDir)
				locations = append(locations, found...)
			}
			if app.outputJSON {
				return app.writeJSON(locations)
			}
			return app.writeLocationsTable(locations)
		},
	}
	command.Flags().StringVar(&options.projectDir, "project", "", "project `<dir>` for project-scoped locations (default: current directory)")
	return command
}

func resolveProjectDir(dir string) (string, error) {
	if dir == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
		return current, nil
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve project directory: %w", err)
	}
	return absolute, nil
}

func selectedLocationHarnesses(args []string) ([]registry.Harness, error) {
	if len(args) == 0 {
		return harness.Supported(), nil
	}
	seen := make(map[registry.Harness]bool, len(args))
	selected := make([]registry.Harness, 0, len(args))
	for _, arg := range args {
		harnessID, err := harness.Parse(arg)
		if err != nil {
			return nil, exitCode(fmt.Errorf("%w %q", registry.ErrUnknownHarness, arg), exitCodeUsage)
		}
		if seen[harnessID] {
			continue
		}
		seen[harnessID] = true
		selected = append(selected, harnessID)
	}
	return selected, nil
}

func (app *application) writeLocationsTable(locations []harness.Location) error {
	const (
		locationHarnessWidth = 10
		locationKindWidth    = 12
		locationScopeWidth   = 7
		locationExistsWidth  = 6
		locationColumns      = 5
		minLocationPathWidth = 36
	)
	pathWidth := minLocationPathWidth
	for _, location := range locations {
		pathWidth = max(pathWidth, text.StringWidth(location.Path))
	}
	fixed := locationHarnessWidth + locationKindWidth + locationScopeWidth + locationExistsWidth + (locationColumns-1)*humanColumnGap
	pathWidth = min(pathWidth, max(minLocationPathWidth, app.maxLineWidth()-fixed))
	rows := make([][]string, 0, len(locations))
	for _, location := range locations {
		rows = append(rows, []string{string(location.Harness), string(location.Kind), string(location.Scope), location.Path, yesNo(location.Exists)})
	}
	return app.writeWrappedHumanTable(
		[]humanColumn{
			{heading: "Harness", width: locationHarnessWidth},
			{heading: "Kind", width: locationKindWidth},
			{heading: "Scope", width: locationScopeWidth},
			{heading: "Path", width: pathWidth, wrap: wrapHumanPath},
			{heading: "Exists", width: locationExistsWidth},
		},
		rows,
	)
}
