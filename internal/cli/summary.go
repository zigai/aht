package cli

import (
	"cmp"
	"context"
	"strconv"

	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (app *application) runListSummary(
	ctx context.Context,
	opts listOptions,
	f registry.Filter,
) error {
	groupBy := registry.SummaryGroupBy(opts.groupBy)

	s, e := app.registryStore().
		SummaryWithOptions(ctx, f, registry.SummaryOptions{GroupBy: groupBy})
	if e != nil {
		return e //nolint:wrapcheck // Client.SummaryWithOptions supplies the operation context.
	}

	if app.outputJSON {
		return app.writeJSON(s)
	}

	return app.writeSummaryTableForGroup(s, groupBy, opts.full)
}

func (app *application) writeSummaryTableForGroup(
	ss []registry.Summary,
	groupBy registry.SummaryGroupBy,
	full bool,
) error {
	switch groupBy {
	case registry.SummaryGroupByProject:
		return app.writeProjectSummaryTable(ss, full)
	case registry.SummaryGroupByHarness:
		return app.writeHarnessSummaryTable(ss, full)
	case registry.SummaryGroupByMultiplexerSession:
		fallthrough
	default:
		return app.writeMultiplexerSummaryTable(ss, full)
	}
}

func (app *application) writeMultiplexerSummaryTable(ss []registry.Summary, full bool) error {
	const (
		summaryMuxWidth     = 10
		summarySessionWidth = 20
		summaryUnknownWidth = 6
	)

	labels := summaryTableLabels(ss)

	rows := make([][]string, 0, len(ss))
	for i, s := range ss {
		row := append(
			[]string{multiplexerSummaryKind(s), labels[i], s.MultiplexerServerID},
			summaryCountCells(s)...,
		)
		rows = append(rows, row)
	}

	columns := append([]humanColumn{
		{heading: "MUX", width: summaryMuxWidth},
		{heading: "Session", width: summarySessionWidth, wrap: wrapHumanSession},
		{heading: "Server", width: summaryUnknownWidth, wrap: wrapHumanIdentifier},
	}, summaryCountColumns()...)

	return app.renderSummaryColumnsAndRows(columns, rows, full)
}

func (app *application) writeProjectSummaryTable(ss []registry.Summary, full bool) error {
	const (
		summaryProjectWidth = 16
		summaryPathWidth    = 24
	)

	rows := make([][]string, 0, len(ss))
	for _, s := range ss {
		projectLabel := s.GroupLabel
		if projectLabel == "" {
			projectLabel = s.Project
		}

		if projectLabel == "" {
			projectLabel = "unknown"
		}

		projectPath := s.ProjectRoot
		if projectPath == "" {
			projectPath = "-"
		}

		row := append([]string{projectLabel, projectPath}, summaryCountCells(s)...)
		rows = append(rows, row)
	}

	columns := append([]humanColumn{
		{heading: "Project", width: summaryProjectWidth, wrap: wrapHumanSession},
		{heading: "Root", width: summaryPathWidth, wrap: wrapHumanPath},
	}, summaryCountColumns()...)

	return app.renderSummaryColumnsAndRows(columns, rows, full)
}

func (app *application) writeHarnessSummaryTable(ss []registry.Summary, full bool) error {
	const (
		summaryAgentWidth = 12
	)

	rows := make([][]string, 0, len(ss))
	for _, s := range ss {
		agentLabel := s.GroupLabel
		if agentLabel == "" {
			agentLabel = string(s.Harness)
		}

		if agentLabel == "" {
			agentLabel = "unknown"
		}

		row := append([]string{agentLabel}, summaryCountCells(s)...)
		rows = append(rows, row)
	}

	columns := append([]humanColumn{
		{heading: "Agent", width: summaryAgentWidth},
	}, summaryCountColumns()...)

	return app.renderSummaryColumnsAndRows(columns, rows, full)
}

func summaryCountCells(s registry.Summary) []string {
	return []string{
		strconv.Itoa(s.Total),
		strconv.Itoa(s.Live),
		strconv.Itoa(s.Gone),
		strconv.Itoa(s.PresenceUnknown),
		strconv.Itoa(s.Running),
		strconv.Itoa(s.Waiting),
		strconv.Itoa(s.Idle),
		strconv.Itoa(s.Failed),
		strconv.Itoa(s.Interrupted),
		strconv.Itoa(s.ActivityUnknown),
	}
}

func summaryCountColumns() []humanColumn {
	const (
		summaryCountWidth   = 5
		summaryUnknownWidth = 6
	)

	return []humanColumn{
		{heading: "Total", width: summaryCountWidth, align: text.AlignRight},
		{heading: "Live", width: summaryCountWidth, align: text.AlignRight},
		{heading: "Gone", width: summaryCountWidth, align: text.AlignRight},
		{heading: "Pres?", width: summaryUnknownWidth, align: text.AlignRight},
		{heading: "Run", width: summaryCountWidth, align: text.AlignRight},
		{heading: "Wait", width: summaryCountWidth, align: text.AlignRight},
		{heading: "Idle", width: summaryCountWidth, align: text.AlignRight},
		{heading: "Failed", width: summaryUnknownWidth, align: text.AlignRight},
		{heading: "Interrupted", width: len("Interrupted"), align: text.AlignRight},
		{heading: "Act?", width: summaryCountWidth, align: text.AlignRight},
	}
}

func (app *application) renderSummaryColumnsAndRows(
	columns []humanColumn,
	rows [][]string,
	full bool,
) error {
	for columnIndex := range columns {
		for _, row := range rows {
			columns[columnIndex].width = max(
				columns[columnIndex].width,
				text.StringWidth(row[columnIndex]),
			)
		}
	}

	if validateHumanColumns(columns, app.maxLineWidth()) != nil {
		return app.writeStackedHumanRows(columns, rows)
	}

	if full {
		return app.writeWrappedHumanTable(columns, rows)
	}

	return app.writeHumanTable(columns, rows)
}

func summaryTableLabels(ss []registry.Summary) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, cmp.Or(s.MultiplexerSessionName, s.MultiplexerSessionID, "unknown"))
	}

	return out
}
