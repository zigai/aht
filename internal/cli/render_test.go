package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"github.com/jedib0t/go-pretty/v6/text"
)

func TestHumanRenderersBoundEveryLine(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	app := &application{stdout: &stdout}

	longValue := strings.Repeat("very-long-value-", 40)
	if err := app.writeHumanTable(
		[]humanColumn{{heading: "FIRST", width: 20}, {heading: "SECOND", width: 98}},
		[][]string{{longValue, longValue}},
	); err != nil {
		t.Fatal(err)
	}

	if err := app.writeWrappedHumanTable(
		[]humanColumn{{heading: "FIRST", width: 20}, {heading: "SECOND", width: 98}},
		[][]string{{longValue, longValue}},
	); err != nil {
		t.Fatal(err)
	}

	if err := app.writeHumanDetails(
		[]humanDetail{{label: "Long value", value: longValue}},
	); err != nil {
		t.Fatal(err)
	}

	assertHumanLinesBounded(t, stdout.String())

	if !strings.Contains(stdout.String(), "…") {
		t.Fatalf("compact table did not mark truncation: %q", stdout.String())
	}
}

func TestSanitizeHumanTextNeutralizesTerminalControls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "printable unicode", input: "hello \u4e16\u754c", want: "hello \u4e16\u754c"},
		{name: "whitespace", input: "hello\n\tworld", want: "hello world"},
		{name: "CSI", input: "\x1b[31mred\x1b[0m", want: "[31mred [0m"},
		{name: "OSC BEL", input: "before\x1b]0;owned\x07after", want: "before ]0;owned after"},
		{
			name:  "OSC ST",
			input: "before\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\after",
			want:  "before ]8;;https://example.com \\link ]8;; \\after",
		},
		{
			name:  "DCS",
			input: "before\x1bP1;2|payload\x1b\\after",
			want:  "before P1;2|payload \\after",
		},
		{name: "APC", input: "before\x1b_payload\x1b\\after", want: "before _payload \\after"},
		{name: "PM", input: "before\x1b^message\x1b\\after", want: "before ^message \\after"},
		{name: "SOS", input: "before\x1bXmessage\x1b\\after", want: "before Xmessage \\after"},
		{name: "C1", input: "before\u009b31mafter", want: "before 31mafter"},
		{name: "incomplete escape", input: "before\x1b[after", want: "before [after"},
		{name: "bidi formatting", input: "left\u202eright\u2066end", want: "left right end"},
		{name: "null and delete", input: "before\x00middle\x7fafter", want: "before middle after"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeHumanText(test.input)
			if got != test.want {
				t.Fatalf("sanitizeHumanText(%q) = %q, want %q", test.input, got, test.want)
			}

			for _, value := range got {
				if unicode.IsControl(value) || isBidiControl(value) {
					t.Fatalf("sanitized output contains forbidden rune %U: %q", value, got)
				}
			}
		})
	}
}

func TestSanitizeHumanTextNeutralizesEveryC0AndC1Control(t *testing.T) {
	t.Parallel()

	for character := rune(0); character <= '\u009f'; character++ {
		if character > '\u001f' && character < '\u007f' {
			continue
		}

		got := sanitizeHumanText("before" + string(character) + "after")
		if got != "before after" {
			t.Fatalf("control %U sanitized to %q, want %q", character, got, "before after")
		}
	}
}

func TestGoPrettyStyleRendering(t *testing.T) {
	t.Parallel()

	app := &application{}

	var out bytes.Buffer

	app.stdout = &out
	cols := []humanColumn{
		{heading: "ID", width: 10},
		{heading: "Agent", width: 10},
		{heading: "Status", width: 10},
	}

	rows := [][]string{
		{"omp-1234", "omp", "running"},
		{"pi-5678", "pi", "idle"},
	}
	if err := app.writeHumanTable(cols, rows); err != nil {
		t.Fatal(err)
	}

	output := out.String()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 4 || lines[1] == "" || strings.Trim(lines[1], "─") != "" {
		t.Fatalf("table lacks a continuous thin header rule: %q", output)
	}

	if strings.ContainsAny(output, "│+|") {
		t.Fatalf("table contains vertical borders: %q", output)
	}
}

func assertHumanLinesBounded(t *testing.T, output string) {
	t.Helper()

	for number, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if width := text.StringWidth(line); width > humanLineWidth {
			t.Fatalf("line %d width = %d, want <= %d: %q", number+1, width, humanLineWidth, line)
		}
	}
}

func TestHumanTablesRenderEmptyState(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	app := &application{stdout: &output}
	if err := app.writeHumanTable([]humanColumn{{heading: "ID", width: 12}}, nil); err != nil {
		t.Fatal(err)
	}

	if got := output.String(); got != "No results.\n" {
		t.Fatalf("empty output = %q", got)
	}
}

func TestHumanTablesAlignNumericColumns(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	app := &application{stdout: &output}

	columns := []humanColumn{
		{heading: "name", width: 10},
		{heading: "count", width: 8, align: text.AlignRight},
	}
	if err := app.writeHumanTable(
		columns,
		[][]string{{"small", "1"}, {"large", "200"}},
	); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if text.StringWidth(lines[2]) != text.StringWidth(lines[3]) {
		t.Fatalf("numeric column is not right aligned: %q", output.String())
	}
}

func TestHumanTablesFallbackPreservesCompleteValues(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	app := &application{stdout: &output}
	// Test fixture intentionally uses multibyte CJK characters to verify wrapped table formatting.
	//nolint:gosmopolitan // Intentional locale-sensitive behavior or Unicode fixture; see above.
	cjkChar := "界"
	value := strings.Repeat(cjkChar, 90)

	columns := []humanColumn{{heading: "identity", width: 100}, {heading: "reason", width: 100}}
	if err := app.writeWrappedHumanTable(columns, [][]string{{"target-id", value}}); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	if strings.Contains(got, "…") || !strings.Contains(got, "identity:") ||
		strings.Count(got, cjkChar) != 90 {
		t.Fatalf("stacked fallback lost data: %q", got)
	}

	assertHumanLinesBounded(t, got)
}

func TestHumanWrappingUsesDisplayCells(t *testing.T) {
	t.Parallel()
	//nolint:gosmopolitan // Test table intentionally includes multibyte CJK fixtures to verify cell width wrapping.
	for _, value := range []string{"界世界世界世", "ab界cd界ef", "é/é/界/世界", "📦/🌍/directory"} {
		for _, width := range []int{2, 3, 5, 8} {
			got := truncateHumanText(value, width)
			if text.StringWidth(got) > width {
				t.Fatalf("truncate(%q, %d) = %q", value, width, got)
			}

			for _, wrap := range []humanWrapFunc{wrapHumanText, wrapHumanPath, wrapHumanIdentifier} {
				assertHumanWrapPreservesCells(t, value, width, wrap)
			}
		}
	}
}

func assertHumanWrapPreservesCells(t *testing.T, value string, width int, wrap humanWrapFunc) {
	t.Helper()

	lines := wrap(value, width)
	if strings.Join(lines, "") != value {
		t.Fatalf("wrap(%q, %d) lost data: %q", value, width, lines)
	}

	for _, line := range lines {
		if text.StringWidth(line) > width {
			t.Fatalf("wrap(%q, %d) contains over-wide line %q", value, width, line)
		}
	}
}
