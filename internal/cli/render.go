package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	"unicode"

	prettytable "github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

const (
	humanLineWidth      = 120
	humanColumnGap      = 2
	humanDetailLabelMax = 24
)

var (
	errHumanTableCellCount = errors.New("human table row has the wrong cell count")
	errHumanTableWidth     = errors.New("human table exceeds output width")
)

type humanWrapFunc func(string, int) []string

type humanColumn struct {
	heading string
	width   int
	wrap    humanWrapFunc
	align   text.Align
}

type humanDetail struct {
	label string
	value string
	wrap  humanWrapFunc
}

func (app *application) writeHumanTable(columns []humanColumn, rows [][]string) error {
	return app.writeHumanTableRows(columns, rows, false)
}

func (app *application) writeWrappedHumanTable(columns []humanColumn, rows [][]string) error {
	return app.writeHumanTableRows(columns, rows, true)
}

//nolint:cyclop // standard human table formatting with validation and wrapping branches
func (app *application) writeHumanTableRows(columns []humanColumn, rows [][]string, wrap bool) error {
	for _, row := range rows {
		if len(row) != len(columns) {
			return fmt.Errorf("%w: got %d cells for %d columns", errHumanTableCellCount, len(row), len(columns))
		}
	}
	if len(rows) == 0 {
		return app.writeln("No results.")
	}
	if err := validateHumanColumns(columns, app.maxLineWidth()); err != nil {
		for _, column := range columns {
			if column.width <= 0 {
				return err
			}
		}
		return app.writeStackedHumanRows(columns, rows)
	}
	if !wrap {
		return app.writeDirectHumanTable(columns, rows)
	}
	writer := prettytable.NewWriter()
	style := prettytable.StyleDefault
	style.Box.PaddingLeft = ""
	style.Box.PaddingRight = "  "
	style.Format.Header = text.FormatDefault
	style.Options = prettytable.OptionsNoBordersAndSeparators
	style.Options.SeparateHeader = true
	style.Box.MiddleHorizontal = "─"
	style.Box.MiddleSeparator = "─"
	writer.SetStyle(style)

	header := make(prettytable.Row, len(columns))
	colConfigs := make([]prettytable.ColumnConfig, len(columns))
	for i, col := range columns {
		header[i] = col.heading
		colConfigs[i] = col.tableConfig(i+1, wrap)
	}
	writer.SetColumnConfigs(colConfigs)
	writer.AppendHeader(header)

	for _, row := range rows {
		tableRow := make(prettytable.Row, len(row))
		for i, cell := range row {
			tableRow[i] = sanitizeHumanText(cell)
		}
		writer.AppendRow(tableRow)
	}

	writer.SuppressTrailingSpaces()
	rendered := writer.Render()
	if rendered == "" {
		return nil
	}
	if _, err := fmt.Fprintln(app.stdout, normalizeHumanTableLayout(rendered)); err != nil {
		return fmt.Errorf("write human table: %w", err)
	}
	return nil
}

//nolint:gocognit,cyclop // fast-path direct table streaming without intermediate DOM allocations
func (app *application) writeDirectHumanTable(columns []humanColumn, rows [][]string) error {
	bw := bufio.NewWriter(app.stdout)
	defer func() { _ = bw.Flush() }()

	var headerBuf strings.Builder
	for i, col := range columns {
		if i > 0 {
			headerBuf.WriteString("  ")
		}
		w := col.width
		sw := text.StringWidth(col.heading)
		if sw < w {
			if col.align == text.AlignRight {
				headerBuf.WriteString(strings.Repeat(" ", w-sw))
				headerBuf.WriteString(col.heading)
			} else {
				headerBuf.WriteString(col.heading)
				headerBuf.WriteString(strings.Repeat(" ", w-sw))
			}
		} else {
			headerBuf.WriteString(col.heading)
		}
	}
	headerLine := strings.TrimRight(headerBuf.String(), " ")
	if _, err := bw.WriteString(headerLine + "\n"); err != nil {
		return fmt.Errorf("write table header: %w", err)
	}

	headerWidth := text.StringWidth(headerLine)
	sep := strings.Repeat("─", headerWidth)
	if _, err := bw.WriteString(sep + "\n"); err != nil {
		return fmt.Errorf("write table separator: %w", err)
	}

	var rowBuf strings.Builder
	for _, row := range rows {
		rowBuf.Reset()
		for i, cell := range row {
			if i > 0 {
				rowBuf.WriteString("  ")
			}
			col := columns[i]
			textVal := truncateHumanText(sanitizeHumanText(cell), col.width)
			sw := text.StringWidth(textVal)
			if sw < col.width {
				if col.align == text.AlignRight {
					rowBuf.WriteString(strings.Repeat(" ", col.width-sw))
					rowBuf.WriteString(textVal)
				} else {
					rowBuf.WriteString(textVal)
					rowBuf.WriteString(strings.Repeat(" ", col.width-sw))
				}
			} else {
				rowBuf.WriteString(textVal)
			}
		}
		rowLine := strings.TrimRight(rowBuf.String(), " ")
		if _, err := bw.WriteString(rowLine + "\n"); err != nil {
			return fmt.Errorf("write table row: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush table output: %w", err)
	}
	return nil
}

func normalizeHumanTableLayout(rendered string) string {
	lines := strings.Split(rendered, "\n")
	contentWidth := 0
	separatorIndex := -1
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
		if separatorIndex == -1 && strings.Trim(line, "─") == "" {
			separatorIndex = i
			continue
		}
		contentWidth = max(contentWidth, text.StringWidth(lines[i]))
	}
	if separatorIndex >= 0 {
		lines[separatorIndex] = strings.Repeat("─", contentWidth)
	}
	return strings.Join(lines, "\n")
}

func (col humanColumn) tableConfig(number int, wrap bool) prettytable.ColumnConfig {
	config := prettytable.ColumnConfig{
		Number:           number,
		WidthMax:         col.width,
		Align:            col.align,
		AlignHeader:      col.align,
		WidthMaxEnforcer: truncateHumanText,
	}
	if !wrap {
		return config
	}
	wrapCell := col.wrap
	if wrapCell == nil {
		wrapCell = wrapHumanText
	}
	config.WidthMaxEnforcer = func(value string, maxLen int) string {
		return strings.Join(wrapCell(value, maxLen), "\n")
	}
	return config
}

func (app *application) writeStackedHumanRows(columns []humanColumn, rows [][]string) error {
	if len(rows) == 0 {
		return app.writeln("No results.")
	}
	for rowIndex, row := range rows {
		if len(row) != len(columns) {
			return fmt.Errorf("%w: got %d cells for %d columns", errHumanTableCellCount, len(row), len(columns))
		}
		details := make([]humanDetail, len(columns))
		for columnIndex, column := range columns {
			details[columnIndex] = humanDetail{
				label: column.heading,
				value: row[columnIndex],
				wrap:  column.wrap,
			}
		}
		if err := app.writeHumanDetails(details); err != nil {
			return err
		}
		if rowIndex+1 < len(rows) {
			if err := app.writeln(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (app *application) maxLineWidth() int {
	if app != nil && app.stdout != nil {
		if w := terminalWidth(app.stdout); w > 0 {
			return w
		}
	}
	return humanLineWidth
}

func validateHumanColumns(columns []humanColumn, maxLineWidth int) error {
	if maxLineWidth <= 0 {
		maxLineWidth = humanLineWidth
	}
	width := max(0, len(columns)-1) * humanColumnGap
	for _, column := range columns {
		if column.width <= 0 {
			return fmt.Errorf("%w: invalid column width %d", errHumanTableWidth, column.width)
		}
		width += column.width
	}
	if width > maxLineWidth {
		return fmt.Errorf("%w: %d > %d", errHumanTableWidth, width, maxLineWidth)
	}
	return nil
}

func (app *application) writeHumanDetails(details []humanDetail) error {
	labelWidth := 0
	for _, detail := range details {
		labelWidth = max(labelWidth, text.StringWidth(sanitizeHumanText(detail.label))+1)
	}
	maxWidth := app.maxLineWidth()
	labelWidth = min(labelWidth, humanDetailLabelMax, max(1, maxWidth-humanColumnGap-1))
	valueWidth := max(1, maxWidth-labelWidth-humanColumnGap)
	for _, detail := range details {
		label := truncateHumanText(detail.label+":", labelWidth)
		wrapValue := detail.wrap
		if wrapValue == nil {
			wrapValue = wrapHumanText
		}
		lines := wrapValue(detail.value, valueWidth)
		for index, line := range lines {
			if index == 0 {
				padding := strings.Repeat(" ", max(0, labelWidth-text.StringWidth(label)))
				if _, err := fmt.Fprintf(app.stdout, "%s%s  %s\n", label, padding, line); err != nil {
					return fmt.Errorf("write human details: %w", err)
				}
				continue
			}
			if _, err := fmt.Fprintf(app.stdout, "%*s  %s\n", labelWidth, "", line); err != nil {
				return fmt.Errorf("write human details: %w", err)
			}
		}
	}
	return nil
}

func sanitizeHumanText(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || isBidiControl(character) {
			return ' '
		}
		return character
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func isBidiControl(character rune) bool {
	switch character {
	case '\u061c', '\u200e', '\u200f':
		return true
	default:
		return character >= '\u202a' && character <= '\u202e' ||
			character >= '\u2066' && character <= '\u2069'
	}
}

func truncateHumanText(value string, width int) string {
	value = sanitizeHumanText(value)
	if width <= 0 {
		return ""
	}
	if text.StringWidth(value) <= width {
		return value
	}
	if width <= 1 {
		return "…"
	}
	return strings.TrimRight(text.Snip(value, width, "…"), " ")
}

func wrapHumanText(value string, width int) []string {
	value = sanitizeHumanText(value)
	if value == "" {
		return []string{"-"}
	}
	return wrapDelimitedHumanText(value, width, " ")
}

func wrapHumanPath(value string, width int) []string {
	return wrapDelimitedHumanText(value, width, `/\`)
}

func wrapHumanSession(value string, width int) []string {
	if strings.Contains(value, " ") {
		return wrapHumanText(value, width)
	}
	return wrapHumanIdentifier(value, width)
}

func wrapHumanIdentifier(value string, width int) []string {
	return wrapDelimitedHumanText(value, width, "-_./")
}

func wrapDelimitedHumanText(value string, width int, delimiters string) []string {
	value = sanitizeHumanText(value)
	if value == "" {
		return []string{"-"}
	}
	var lines []string
	width = max(1, width)
	for text.StringWidth(value) > width {
		cut, delimiterCut := 0, 0
		for index, character := range value {
			end := index + len(string(character))
			if text.StringWidth(value[:end]) > width {
				// A single wide glyph cannot fit a one-cell viewport; preserve it.
				if cut == 0 {
					cut = end
				}
				break
			}
			cut = end
			if character == ' ' || strings.ContainsRune(delimiters, character) {
				delimiterCut = end
			}
		}
		if delimiterCut > 0 {
			cut = delimiterCut
		}
		lines = append(lines, strings.TrimSpace(value[:cut]))
		value = strings.TrimSpace(value[cut:])
	}
	if value != "" {
		lines = append(lines, value)
	}
	if len(lines) == 0 {
		return []string{"-"}
	}
	return lines
}
