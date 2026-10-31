package history

import (
	"cmp"
	"maps"
	"slices"
	"time"
)

const maxReparsedFiles = 10

// RefreshStats counts inspected files and completed refreshes. Skipped counts
// new or changed files a search left unindexed because its filters exclude them. BytesParsed counts
// native file bytes consumed by completed parses, excluding verified append prefixes;
// for SQLite histories it is the database size, not physical database I/O.
// ParseTime and StoreTime are cumulative worker and transaction time, not wall time.
type RefreshStats struct {
	Checked     int            `json:"checked"`
	Unchanged   int            `json:"unchanged"`
	Skipped     int            `json:"skipped"`
	Resumed     int            `json:"resumed"`
	Reparsed    int            `json:"reparsed"`
	BytesParsed int64          `json:"bytes_parsed"`
	ParseTime   time.Duration  `json:"parse_time"`
	StoreTime   time.Duration  `json:"store_time"`
	Reasons     map[string]int `json:"reasons,omitempty"`
}

type SourceRefresh struct {
	RefreshStats

	Source Source `json:"source"`
}

type ReparsedFile struct {
	Source Source `json:"source"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Reason string `json:"reason"`
}

func (s *search) refreshed(
	source Source,
	path string,
	result refreshResult,
	storeTime time.Duration,
	stats *RefreshStats,
) {
	stats.BytesParsed += result.bytes
	stats.ParseTime += result.parseTime

	stats.StoreTime += storeTime
	if result.writer.resume != nil {
		stats.Resumed++
		return
	}

	stats.Reparsed++
	if stats.Reasons == nil {
		stats.Reasons = map[string]int{}
	}

	stats.Reasons[result.reason]++
	s.result.Reparsed = append(
		s.result.Reparsed,
		ReparsedFile{Source: source, Path: path, Bytes: result.bytes, Reason: result.reason},
	)
	slices.SortFunc(s.result.Reparsed, func(first, second ReparsedFile) int {
		if order := cmp.Compare(second.Bytes, first.Bytes); order != 0 {
			return order
		}

		if order := cmp.Compare(first.Source.Harness, second.Source.Harness); order != 0 {
			return order
		}

		return cmp.Compare(first.Path, second.Path)
	})
	s.result.Reparsed = s.result.Reparsed[:min(maxReparsedFiles, len(s.result.Reparsed))]
}

func (stats RefreshStats) snapshot() RefreshStats {
	stats.Reasons = maps.Clone(stats.Reasons)
	return stats
}
