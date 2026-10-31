package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (app *application) newHistoryCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "history",
		Short:         "Manage the conversation search index",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(app.newHistoryRefreshCommand())

	return command
}

func (app *application) newHistoryRefreshCommand() *cobra.Command {
	var (
		harnesses     []string
		sourceOptions searchOptions
	)

	command := &cobra.Command{
		Use:   "refresh",
		Short: "Index changed conversation histories so searches start warm",
		Long: "Parse every changed native history into the disposable " +
			"search index without searching.\nThe tracker refreshes all " +
			"histories at startup and after sessions go idle or end.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sources, err := sourceOptions.validateSources()
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}

			return app.runHistoryRefresh(cmd.Context(), harnesses, sources)
		},
	}
	command.Flags().
		StringSliceVar(&harnesses, "harness", nil, "refresh only harness `<name,...>` histories")
	command.Flags().
		StringArrayVar(&sourceOptions.sources, "source", nil, "refresh only this native history `<agent=path>`")
	_ = command.Flags().MarkHidden("source")

	return command
}

func (app *application) runHistoryRefresh(
	ctx context.Context,
	names []string,
	sources []history.Source,
) error {
	sources, err := historyRefreshSources(names, sources)
	if err != nil {
		return err
	}

	guard, err := lockHistoryRefresh()
	if errors.Is(err, errHistoryRefreshRunning) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("history refresh: %w", err)
	}

	defer func() { _ = guard.Close() }()

	progress := app.newSearchProgress()
	refreshed := map[history.Source]int{}
	catalog := history.Catalog{Sources: sources, IndexPath: "", Progress: func(p history.Progress) {
		refreshed[p.Source] = p.Refreshed
		progress.update(p)
	}}
	result, refreshErr := catalog.Refresh(ctx)

	progress.clear()

	if app.outputJSON {
		if err := app.writeJSONSafe(result); err != nil {
			return err
		}
	} else {
		files, changed := 0, 0
		for _, status := range result.Sources {
			files += status.Files
		}

		for _, count := range refreshed {
			changed += count
		}

		if err := app.writef(
			"Checked %d histories; indexed %d changed.\n",
			files,
			changed,
		); err != nil {
			return err
		}

		app.writeSearchIssues(result, false)
	}

	if errors.Is(refreshErr, history.ErrIndexUnavailable) {
		return fmt.Errorf("history refresh: %w", refreshErr)
	}

	return searchFailure(refreshErr)
}

func historyRefreshSources(names []string, sources []history.Source) ([]history.Source, error) {
	var selected []registry.Harness

	for _, name := range names {
		id, err := harness.Parse(strings.TrimSpace(name))
		if err != nil {
			return nil, exitCode(fmt.Errorf("history refresh: %w", err), exitCodeUsage)
		}

		selected = append(selected, id)
	}

	if sources == nil {
		var err error

		sources, err = history.DefaultSources()
		if err != nil {
			return nil, fmt.Errorf("history refresh: %w", err)
		}
	}

	if len(selected) > 0 {
		sources = slices.DeleteFunc(
			sources,
			func(source history.Source) bool { return !slices.Contains(selected, source.Harness) },
		)
	}

	return sources, nil
}
