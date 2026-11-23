package aht

import (
	"context"

	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/history"
)

const (
	// HistorySortUpdated orders by most recent activity first.
	HistorySortUpdated = history.SortUpdated
	// HistorySortCreated orders by most recently started conversation first.
	HistorySortCreated = history.SortCreated
	// HistorySortMatches orders search results by most matching message parts.
	HistorySortMatches = history.SortMatches
	// HistorySortMessages orders by most user and assistant messages first.
	HistorySortMessages = history.SortMessages
)

var (
	ErrHistoryIncomplete   = history.ErrIncomplete
	ErrInvalidHistoryQuery = history.ErrInvalidQuery
	// ErrHistoryIndexUnavailable identifies a history index refresh that could
	// not use the disposable index.
	ErrHistoryIndexUnavailable = history.ErrIndexUnavailable
)

type (
	// HistoryQuery configures conversation-content search.
	HistoryQuery = history.Query
	// HistoryListQuery lists conversations by metadata without searching text.
	HistoryListQuery = history.ListQuery
	HistoryFilter    = history.Filter
	HistorySort      = history.Sort
	// HistoryRole is the author of a message part in queries and excerpts.
	HistoryRole = history.Role
	// HistoryStatus is a history source's coverage in a result.
	HistoryStatus = history.Status
	HistoryResult = history.Result
	// HistoryCatalog supports explicit sources instead of default discovery.
	HistoryCatalog = history.Catalog
	// HistorySource identifies a native history directory or SQLite database.
	HistorySource       = history.Source
	HistoryProgress     = history.Progress
	HistoryRefreshStats = history.RefreshStats
	// HistoryRefreshStatus describes the last completed or partial index refresh.
	HistoryRefreshStatus = history.RefreshStatus
	HistorySourceRefresh = history.SourceRefresh
	HistoryReparsedFile  = history.ReparsedFile
)

// SearchHistory searches retained local histories independently of the tracker.
// User and assistant text is included by default; HistoryQuery.IncludeTools
// adds tool calls and tool output. Matching is literal and case-insensitive
// with Unicode case folding unless HistoryQuery.CaseSensitive is set.
//
// The returned [HistoryResult] carries partial matches even when the error is
// non-nil, so callers must inspect both. Errors can be classified with
// [errors.Is]: [ErrInvalidHistoryQuery] reports options rejected before any
// history is read, [ErrHistoryIncomplete] reports sources that could not be
// searched, and [context.Canceled] or [context.DeadlineExceeded] reports an
// ended context.
func SearchHistory(ctx context.Context, query HistoryQuery) (HistoryResult, error) {
	var historyCatalog HistoryCatalog

	result, err := historyCatalog.Search(ctx, query)
	if err != nil {
		return result, err
	}

	return result, nil
}

// ListHistory lists retained local conversations selected by the filter, with
// the same coverage and error contract as [SearchHistory].
func ListHistory(ctx context.Context, query HistoryListQuery) (HistoryResult, error) {
	var historyCatalog HistoryCatalog

	result, err := historyCatalog.List(ctx, query)
	if err != nil {
		return result, err
	}

	return result, nil
}

// ResumeCommand returns the native command that resumes a harness session, to
// run in the session's working directory. sessionPath is the native transcript
// path when known. It returns nil when the harness cannot resume by identity.
func ResumeCommand(harness Harness, sessionID string, sessionPath string) []string {
	return adapters.ResumeCommandFor(harness, sessionID, sessionPath)
}
