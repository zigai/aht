package cli

import (
	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/zigai/aht/v2/pkg/registry"
)

func listColumnMetrics(rows [][]string) ([]string, []int) {
	headings := []string{
		"ID",
		"Agent",
		"Session",
		"Presence",
		"Activity",
		"Location",
		"CWD",
		"Updated",
	}

	maxLen := make([]int, len(headings))
	for i, h := range headings {
		maxLen[i] = text.StringWidth(h)
	}

	for _, row := range rows {
		for i, cell := range row {
			if i < len(maxLen) {
				maxLen[i] = max(maxLen[i], text.StringWidth(cell))
			}
		}
	}

	return headings, maxLen
}

func listTableColumns(rows [][]string, maxWidth int) []humanColumn {
	const (
		maxLocationColumnWidth = 24
	)

	if maxWidth <= 0 {
		maxWidth = humanLineWidth
	}

	headings, maxLen := listColumnMetrics(rows)

	idWidth := maxLen[0]
	agentWidth := maxLen[1]
	presenceWidth := maxLen[3]
	activityWidth := maxLen[4]
	locationWidth := max(len("Location"), min(maxLen[5], maxLocationColumnWidth))
	updatedWidth := max(len("Updated"), maxLen[7])

	gapsTotal := (len(headings) - 1) * humanColumnGap
	fixedTotal := idWidth + agentWidth + presenceWidth + activityWidth + locationWidth + updatedWidth + gapsTotal

	sessionNeeded := max(len("Session"), maxLen[2])
	cwdNeeded := max(len("CWD"), maxLen[6])
	available := maxWidth - fixedTotal

	var sessionWidth, cwdWidth int

	switch {
	case available >= sessionNeeded+cwdNeeded:
		sessionWidth = sessionNeeded
		cwdWidth = cwdNeeded
	case available > 0:
		totalNeeded := sessionNeeded + cwdNeeded
		sessionWidth = max(len("Session"), (available*sessionNeeded)/totalNeeded)

		cwdWidth = max(len("CWD"), available-sessionWidth)
		if cwdWidth > cwdNeeded {
			sessionWidth += cwdWidth - cwdNeeded
			cwdWidth = cwdNeeded
		}
	default:
		sessionWidth = len("Session")
		cwdWidth = len("CWD")
	}

	return []humanColumn{
		{heading: "ID", width: idWidth},
		{heading: "Agent", width: agentWidth},
		{heading: "Session", width: sessionWidth},
		{heading: "Presence", width: presenceWidth},
		{heading: "Activity", width: activityWidth},
		{heading: "Location", width: locationWidth},
		{heading: "CWD", width: cwdWidth},
		{heading: "Updated", width: updatedWidth},
	}
}

func listFullTableColumns(rows [][]string, maxWidth int) ([]humanColumn, bool) {
	const (
		sessionMinWidth = 24
		cwdMinWidth     = 20
	)

	headings, maxLen := listColumnMetrics(rows)
	sessionNeeded := maxLen[2]
	cwdNeeded := maxLen[6]
	sessionWidth := min(sessionNeeded, max(len("Session"), sessionMinWidth))
	cwdWidth := min(cwdNeeded, max(len("CWD"), cwdMinWidth))
	fixedWidth := maxLen[0] + maxLen[1] + maxLen[3] + maxLen[4] + maxLen[5] + maxLen[7]
	available := maxWidth - fixedWidth - (len(headings)-1)*humanColumnGap

	fits := available >= sessionWidth+cwdWidth
	if fits {
		sessionWidth, cwdWidth = allocateColumnPair(
			sessionWidth,
			cwdWidth,
			sessionNeeded,
			cwdNeeded,
			available,
		)
	}

	return []humanColumn{
		{heading: "ID", width: maxLen[0], wrap: wrapHumanIdentifier},
		{heading: "Agent", width: maxLen[1]},
		{heading: "Session", width: sessionWidth, wrap: wrapHumanSession},
		{heading: "Presence", width: maxLen[3]},
		{heading: "Activity", width: maxLen[4]},
		{heading: "Location", width: maxLen[5], wrap: wrapHumanIdentifier},
		{heading: "CWD", width: cwdWidth, wrap: wrapHumanPath},
		{heading: "Updated", width: maxLen[7]},
	}, fits
}

func listActivity(session registry.Session) string {
	if session.Presence() == registry.PresenceGone {
		return "-"
	}

	return formatActivity(session.Activity())
}
