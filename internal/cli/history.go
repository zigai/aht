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
	var harnesses []string
	command := &cobra.Command{
		Use:           "refresh",
		Short:         "Index changed conversation histories so searches start warm",
		Long:          "Parse every changed native history into the disposable search index without searching.\nThe tracker also refreshes sessions as they go idle or end.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runHistoryRefresh(cmd.Context(), harnesses)
		},
	}
	command.Flags().StringSliceVar(&harnesses, "harness", nil, "refresh only harness `<name,...>` histories")
	return command
}

func (app *application) runHistoryRefresh(ctx context.Context, names []string) error {
	var selected []registry.Harness
	for _, name := range names {
		id, err := harness.Parse(strings.TrimSpace(name))
		if err != nil {
			return exitCode(fmt.Errorf("history refresh: %w", err), exitCodeUsage)
		}
		selected = append(selected, id)
	}
	sources, err := history.DefaultSources()
	if err != nil {
		return fmt.Errorf("history refresh: %w", err)
	}
	if len(selected) > 0 {
		sources = slices.DeleteFunc(sources, func(source history.Source) bool { return !slices.Contains(selected, source.Harness) })
	}
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
		if err := app.writef("Checked %d histories; indexed %d changed.\n", files, changed); err != nil {
			return err
		}
		app.writeSearchIssues(result, false)
	}
	if errors.Is(refreshErr, history.ErrIndexUnavailable) {
		return fmt.Errorf("history refresh: %w", refreshErr)
	}
	return searchFailure(refreshErr)
}

// runHistoryIndexer keeps the search index warm from the tracker: whenever a
// session with a native history path goes idle or ends, its history is
// refreshed. Index contention with a concurrent search is skipped; the next
// change or search refreshes it.
func (app *application) runHistoryIndexer(ctx context.Context, store *registry.MemoryStore, quiet bool) error {
	indexed := map[string]string{}
	var revision uint64
	for {
		state, err := store.WaitForRevision(ctx, revision, registry.Filter{})
		if err != nil {
			return historyWatchError(ctx, err)
		}
		revision = state.Revision
		var sources []history.Source
		sources, indexed = unindexedHistorySources(app.cfg.Filter.IgnoreHarnesses, state.Sessions, indexed)
		if len(sources) > 0 {
			app.refreshHistorySources(ctx, sources, quiet)
		}
	}
}

func historyWatchError(ctx context.Context, err error) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return fmt.Errorf("watch sessions for history indexing: %w", err)
	}
}

func unindexedHistorySources(ignored []string, sessions []registry.Session, indexed map[string]string) ([]history.Source, map[string]string) {
	var sources []history.Source
	current := make(map[string]string, len(sessions))
	for _, session := range sessions {
		if !historySettled(session) || slices.Contains(ignored, string(session.Harness)) {
			continue
		}
		stamp := session.UpdatedAt.String()
		current[session.ID] = stamp
		if indexed[session.ID] == stamp {
			continue
		}
		source := history.Source{Harness: session.Harness, Path: session.SessionPath}
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}
	return sources, current
}

func (app *application) refreshHistorySources(ctx context.Context, sources []history.Source, quiet bool) {
	catalog := history.Catalog{Sources: sources, IndexPath: "", Progress: nil}
	_, err := catalog.Refresh(ctx)
	if err != nil && ctx.Err() == nil && !quiet && !errors.Is(err, history.ErrIndexUnavailable) && !errors.Is(err, history.ErrIncomplete) {
		app.warnf("warning: history indexing: %s\n", sanitizeHumanText(err.Error()))
	}
}

// historySettled reports a session whose native history stopped changing for
// now: its turn finished or the session ended.
func historySettled(session registry.Session) bool {
	if session.SessionPath == "" {
		return false
	}
	if session.Presence() == registry.PresenceGone {
		return true
	}
	activity := session.Activity()
	return activity != nil && *activity == registry.ActivityIdle
}
