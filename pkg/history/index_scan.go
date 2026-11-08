package history

import (
	"context"
	"time"
)

type indexScan struct {
	*queryPlan

	sourceMetadata            map[string]string
	failure                   func(Source, string, error)
	refresh                   []SourceRefresh
	keep                      func(context.Context, Match, int64)
	halted                    func() bool
	report                    func(Progress)
	issue                     func(Source, string, error)
	fileIssue                 func(Source, string, error)
	reportIssues              func(string, []Issue, int, map[string]bool)
	refreshed                 func(Source, string, refreshResult, time.Duration, *RefreshStats)
	issueConversationSelected func(Conversation, bool) bool
	prefilterMembers          func(context.Context, historyFile) bool
}

func (s *search) scanIndex(
	ctx context.Context,
	source Source,
	files []historyFile,
	status *SourceStatus,
) {
	scan := s.indexScan()
	s.index.scanFiles(ctx, scan, source, files, status)
	s.result.Refresh = append(s.result.Refresh, scan.refresh...)
}

func (s *search) indexScan() *indexScan {
	return &indexScan{
		queryPlan:                 &s.queryPlan,
		sourceMetadata:            s.sourceMetadata,
		failure:                   s.reportIndexError,
		refresh:                   nil,
		keep:                      s.keep,
		halted:                    s.halted,
		report:                    s.report,
		fileIssue:                 s.fileIssue,
		issue:                     s.issue,
		reportIssues:              s.reportIssues,
		refreshed:                 s.refreshed,
		issueConversationSelected: s.issueConversationSelected,
		prefilterMembers:          s.prefilterMembers,
	}
}
