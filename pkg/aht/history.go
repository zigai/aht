package aht

import (
	"context"

	"github.com/zigai/aht/pkg/history"
)

var (
	// ErrHistoryIncomplete identifies searches that returned partial results.
	ErrHistoryIncomplete = history.ErrIncomplete
	// ErrInvalidHistoryQuery identifies invalid search options.
	ErrInvalidHistoryQuery = history.ErrInvalidQuery
)

type (
	// HistoryQuery configures literal conversation-content search.
	HistoryQuery = history.Query
	// HistoryResult includes matches and source coverage.
	HistoryResult = history.Result
	// HistoryCatalog supports explicit sources instead of default discovery.
	HistoryCatalog = history.Catalog
	// HistorySource identifies a native history directory or SQLite database.
	HistorySource = history.Source
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
	result, err := history.Search(ctx, query)
	if err != nil {
		// history already prefixes errors with "search history:", so wrapcheck
		// would add a second, redundant layer.
		//nolint:wrapcheck // the returned error already carries operation context
		return result, err
	}
	return result, nil
}
