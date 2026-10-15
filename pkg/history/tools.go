package history

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *search) scanToolsFiles(ctx context.Context, source Source, files []historyFile, status *SourceStatus) {
	metadata := new(search)
	metadata.mode = modeList
	metadata.query = (ListQuery{Filter: s.query.Filter, Sort: "", Limit: 0}).searchQuery()
	metadata.explicitSources = s.explicitSources
	metadata.sourceMetadata = s.sourceMetadata
	metadata.registry = s.registry
	metadata.progress = s.progress
	metadata.reset()
	metadata.result.IndexUnavailable = s.result.IndexUnavailable
	metadata.metadataFailures = make(map[string]bool)
	if s.index != nil {
		s.index.scanFiles(ctx, metadata, source, files, status)
	} else {
		metadata.scanFilesParallel(ctx, source, files, status)
	}
	failures := s.failureCount()
	for _, issue := range metadata.result.Issues {
		s.recordIssue(issue)
	}
	s.failures = failures + metadata.failures
	s.result.OmittedIssues += metadata.result.OmittedIssues
	s.result.Refresh = append(s.result.Refresh, metadata.result.Refresh...)
	s.result.Reparsed = append(s.result.Reparsed, metadata.result.Reparsed...)
	s.indexErr = errors.Join(s.indexErr, metadata.indexErr)
	if ctx.Err() != nil || s.indexErr != nil {
		return
	}
	selected := make(map[string]bool, len(metadata.result.Matches))
	for _, match := range metadata.result.Matches {
		selected[match.Conversation.Path] = true
	}
	var candidates []historyFile
	for _, file := range files {
		candidate := selected[file.path]
		for _, child := range file.children {
			candidate = candidate || selected[child.path]
		}
		if candidate {
			candidates = append(candidates, file)
		}
	}
	s.toolsFailures = metadata.metadataFailures
	defer func() { s.toolsFailures = nil }()
	var scanned SourceStatus
	s.scanFilesParallel(ctx, source, candidates, &scanned)
}

func (s *search) matchesFamily(ctx context.Context, file historyFile) bool {
	if s.prefilter == nil {
		return true
	}
	var filters []*bytePrefilter
	for _, term := range s.terms {
		if filter := newBytePrefilter([]matcher{term}); filter != nil {
			filters = append(filters, filter)
		}
	}
	remaining := len(filters)
	for _, member := range append([]historyFile{file}, file.children...) {
		if remaining == 0 || member.database || strings.HasSuffix(member.path, ".zst") {
			return true
		}
		matched, err := member.matchCandidateTerms(ctx, filters)
		if err != nil {
			return true
		}
		remaining -= matched
	}
	return remaining == 0
}

func (file historyFile) matchCandidateTerms(ctx context.Context, filters []*bytePrefilter) (int, error) {
	opened, err := file.open()
	if err != nil {
		return 0, fmt.Errorf("open tools candidate: %w", err)
	}
	matched := 0
	for index, filter := range filters {
		if filter == nil {
			continue
		}
		var candidate bool
		candidate, err = filter.matchesFile(ctx, opened)
		if err != nil {
			break
		}
		if candidate {
			filters[index] = nil
			matched++
		}
	}
	if err = errors.Join(err, opened.Close()); err != nil {
		return matched, fmt.Errorf("read tools candidate: %w", err)
	}
	return matched, nil
}

func (s *search) reportToolsIssues(buffer *fileIssueBuffer) {
	for _, issues := range buffer.issues {
		for _, issue := range issues {
			if issue.Record || s.toolsFailures[issue.Path] {
				continue
			}
			if s.explicitFileSource(issue.Source) || s.fileIssueSelected(issue.Source, issue.Path) {
				s.recordIssue(issue)
			} else {
				s.mu.Lock()
				s.result.OmittedIssues++
				s.mu.Unlock()
			}
		}
	}
}
