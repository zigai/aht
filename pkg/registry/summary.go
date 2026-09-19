package registry

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
)

type summaryGroupKey struct {
	groupBy     SummaryGroupBy
	kind        MultiplexerKind
	server      string
	id          string
	name        string
	projectRoot string
	harness     Harness
}

// SummariesWithOptions aggregates sessions into summaries according to opts.
func SummariesWithOptions(sessions []Session, opts SummaryOptions) []Summary {
	groupBy := opts.GroupBy
	if groupBy == "" {
		groupBy = SummaryGroupByMultiplexerSession
	}

	byKey := make(map[summaryGroupKey]*Summary)
	order := make([]summaryGroupKey, 0)

	for _, session := range sessions {
		populateMultiplexerProjection(&session)

		key, label, keyStr := sessionGroupKey(session, groupBy)
		summary := byKey[key]
		if summary == nil {
			summary = newSummaryForGroup(session, groupBy, key, label, keyStr)
			byKey[key] = summary
			order = append(order, key)
		}
		summary.addSession(session)
	}

	result := make([]Summary, 0, len(order))
	for _, key := range order {
		result = append(result, *byKey[key])
	}
	sortSummaries(result, groupBy)
	return result
}

func sessionGroupKey(session Session, groupBy SummaryGroupBy) (summaryGroupKey, string, string) {
	switch groupBy {
	case SummaryGroupByProject:
		return projectSummaryKey(session)
	case SummaryGroupByHarness:
		return harnessSummaryKey(session)
	case SummaryGroupByMultiplexerSession:
		fallthrough
	default:
		return multiplexerSummaryKey(session)
	}
}

func multiplexerSummaryKey(session Session) (summaryGroupKey, string, string) {
	populateMultiplexerProjection(&session)
	key := summaryGroupKey{
		groupBy:     SummaryGroupByMultiplexerSession,
		kind:        session.Multiplexer.Kind,
		server:      session.Multiplexer.ServerID,
		id:          "",
		name:        "",
		projectRoot: "",
		harness:     "",
	}
	if session.Multiplexer.SessionID != "" {
		key.id = session.Multiplexer.SessionID
	} else {
		key.name = session.Multiplexer.SessionName
	}

	var label string
	switch {
	case session.Multiplexer.SessionName != "":
		label = session.Multiplexer.SessionName
	case session.Multiplexer.SessionID != "":
		label = session.Multiplexer.SessionID
	case session.Tmux.SessionName != "":
		label = session.Tmux.SessionName
	case session.Tmux.SessionID != "":
		label = session.Tmux.SessionID
	default:
		label = "unknown"
	}

	idOrName := cmp.Or(key.id, key.name)
	var keyStr string
	switch {
	case key.server != "":
		keyStr = string(key.kind) + ":" + key.server + ":" + idOrName
	case idOrName != "":
		keyStr = string(key.kind) + ":" + idOrName
	default:
		keyStr = "unknown"
	}

	return key, label, keyStr
}

func projectSummaryKey(session Session) (summaryGroupKey, string, string) {
	if session.ProjectRoot == "" {
		key := summaryGroupKey{
			groupBy:     SummaryGroupByProject,
			kind:        "",
			server:      "",
			id:          "",
			name:        "",
			projectRoot: "",
			harness:     "",
		}
		return key, "unknown", ""
	}

	cleaned := filepath.Clean(session.ProjectRoot)
	key := summaryGroupKey{
		groupBy:     SummaryGroupByProject,
		kind:        "",
		server:      "",
		id:          "",
		name:        "",
		projectRoot: cleaned,
		harness:     "",
	}

	label := filepath.Base(cleaned)
	if label == "" || label == "." || label == "/" {
		label = cleaned
	}

	return key, label, cleaned
}

func harnessSummaryKey(session Session) (summaryGroupKey, string, string) {
	key := summaryGroupKey{
		groupBy:     SummaryGroupByHarness,
		kind:        "",
		server:      "",
		id:          "",
		name:        "",
		projectRoot: "",
		harness:     session.Harness,
	}
	label := string(session.Harness)
	if label == "" {
		label = "unknown"
	}
	return key, label, string(session.Harness)
}

func newSummaryForGroup(
	session Session,
	groupBy SummaryGroupBy,
	key summaryGroupKey,
	label string,
	keyStr string,
) *Summary {
	summary := &Summary{
		GroupBy:                groupBy,
		GroupKey:               keyStr,
		GroupLabel:             label,
		Project:                "",
		ProjectRoot:            "",
		Harness:                "",
		MultiplexerKind:        "",
		MultiplexerServerID:    "",
		MultiplexerSessionID:   "",
		MultiplexerSessionName: "",
		TmuxSessionID:          "",
		TmuxSessionName:        "",
		Total:                  0,
		Live:                   0,
		Gone:                   0,
		PresenceUnknown:        0,
		Running:                0,
		Waiting:                0,
		Idle:                   0,
		Failed:                 0,
		Interrupted:            0,
		ActivityUnknown:        0,
	}

	switch groupBy {
	case SummaryGroupByProject:
		summary.Project = label
		summary.ProjectRoot = key.projectRoot
	case SummaryGroupByHarness:
		summary.Harness = session.Harness
	case SummaryGroupByMultiplexerSession:
		fallthrough
	default:
		summary.MultiplexerKind = session.Multiplexer.Kind
		summary.MultiplexerServerID = session.Multiplexer.ServerID
		summary.MultiplexerSessionID = session.Multiplexer.SessionID
		summary.MultiplexerSessionName = session.Multiplexer.SessionName
		summary.TmuxSessionID = session.Tmux.SessionID
		summary.TmuxSessionName = session.Tmux.SessionName
	}

	return summary
}

func sortSummaries(summaries []Summary, groupBy SummaryGroupBy) {
	switch groupBy {
	case SummaryGroupByMultiplexerSession:
		sortMultiplexerSummaries(summaries)
	case SummaryGroupByProject:
		sortProjectSummaries(summaries)
	case SummaryGroupByHarness:
		sortHarnessSummaries(summaries)
	default:
		slices.SortFunc(summaries, func(a, b Summary) int {
			return cmp.Compare(a.GroupKey, b.GroupKey)
		})
	}
}

func sortMultiplexerSummaries(summaries []Summary) {
	slices.SortFunc(summaries, func(a, b Summary) int {
		aEmpty := a.MultiplexerKind == "" && a.MultiplexerServerID == "" &&
			a.MultiplexerSessionID == "" && a.MultiplexerSessionName == ""
		bEmpty := b.MultiplexerKind == "" && b.MultiplexerServerID == "" &&
			b.MultiplexerSessionID == "" && b.MultiplexerSessionName == ""
		if aEmpty != bEmpty {
			if aEmpty {
				return 1
			}
			return -1
		}
		return cmp.Or(
			cmp.Compare(a.MultiplexerKind, b.MultiplexerKind),
			cmp.Compare(a.MultiplexerServerID, b.MultiplexerServerID),
			cmp.Compare(a.MultiplexerSessionName, b.MultiplexerSessionName),
			cmp.Compare(a.MultiplexerSessionID, b.MultiplexerSessionID),
			cmp.Compare(a.TmuxSessionName, b.TmuxSessionName),
			cmp.Compare(a.TmuxSessionID, b.TmuxSessionID),
			cmp.Compare(a.GroupKey, b.GroupKey),
		)
	})
}

func sortProjectSummaries(summaries []Summary) {
	slices.SortFunc(summaries, func(a, b Summary) int {
		aEmpty := a.ProjectRoot == ""
		bEmpty := b.ProjectRoot == ""
		if aEmpty != bEmpty {
			if aEmpty {
				return 1
			}
			return -1
		}
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.GroupLabel), strings.ToLower(b.GroupLabel)),
			cmp.Compare(a.GroupLabel, b.GroupLabel),
			cmp.Compare(a.ProjectRoot, b.ProjectRoot),
			cmp.Compare(a.GroupKey, b.GroupKey),
		)
	})
}

func sortHarnessSummaries(summaries []Summary) {
	slices.SortFunc(summaries, func(a, b Summary) int {
		aEmpty := a.Harness == ""
		bEmpty := b.Harness == ""
		if aEmpty != bEmpty {
			if aEmpty {
				return 1
			}
			return -1
		}
		return cmp.Or(
			cmp.Compare(strings.ToLower(string(a.Harness)), strings.ToLower(string(b.Harness))),
			cmp.Compare(string(a.Harness), string(b.Harness)),
			cmp.Compare(a.GroupKey, b.GroupKey),
		)
	})
}

func (s *Summary) addSession(session Session) {
	s.Total++
	switch session.Presence {
	case PresenceLive:
		s.Live++
	case PresenceGone:
		s.Gone++
	case PresenceUnknown:
		fallthrough
	default:
		s.PresenceUnknown++
	}
	if session.Presence == PresenceGone {
		return
	}
	s.addActivity(session.Activity)
}

func (s *Summary) addActivity(activity *Activity) {
	if activity == nil {
		s.ActivityUnknown++
		return
	}
	switch *activity {
	case ActivityRunning:
		s.Running++
	case ActivityWaiting:
		s.Waiting++
	case ActivityIdle:
		s.Idle++
	case ActivityFailed:
		s.Failed++
	case ActivityInterrupted:
		s.Interrupted++
	case ActivityUnknown:
		fallthrough
	default:
		s.ActivityUnknown++
	}
}

func summariesForSessions(sessions []Session) []Summary {
	return SummariesWithOptions(sessions, SummaryOptions{GroupBy: SummaryGroupByMultiplexerSession})
}
