package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

type jsonOutput struct {
	writer io.Writer
}

func (out jsonOutput) Write(data []byte) (int, error) {
	safe := escapeBidi(data)

	n, err := out.writer.Write(safe)
	if err != nil {
		return 0, fmt.Errorf("write JSON output: %w", err)
	}

	if n != len(safe) {
		return 0, io.ErrShortWrite
	}

	return len(data), nil
}

func (app *application) writeJSON(value any) error {
	encoder := json.NewEncoder(jsonOutput{writer: app.stdout})
	encoder.SetIndent("", jsonIndent)

	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	return nil
}

func (app *application) writeJSONLine(value any) error {
	if err := json.NewEncoder(jsonOutput{writer: app.stdout}).Encode(value); err != nil {
		return fmt.Errorf("writing JSON line: %w", err)
	}

	return nil
}

func escapeBidi(data []byte) []byte {
	offset := bytes.IndexFunc(data, isBidiControl)
	if offset < 0 {
		return data
	}

	safe := make([]byte, 0, len(data))
	for offset >= 0 {
		safe = append(safe, data[:offset]...)
		character, size := utf8.DecodeRune(data[offset:])
		quoted := strconv.QuoteRuneToASCII(character)
		safe = append(safe, quoted[1:len(quoted)-1]...)
		data = data[offset+size:]
		offset = bytes.IndexFunc(data, isBidiControl)
	}

	return append(safe, data...)
}

func (app *application) writef(format string, args ...any) error {
	if _, err := fmt.Fprintf(app.stdout, format, args...); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

func (app *application) writeln(args ...any) error {
	if _, err := fmt.Fprintln(app.stdout, args...); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

func (app *application) warnf(format string, args ...any) {
	if app.stderr != nil {
		_, _ = fmt.Fprintf(app.stderr, format, args...)
	}
}
