package cli

import (
	"encoding/json"
	"fmt"
)

func (app *application) writeJSON(value any) error {
	e := json.NewEncoder(app.stdout)
	e.SetIndent("", jsonIndent)

	if err := e.Encode(value); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	return nil
}

func (app *application) writeJSONLine(value any) error {
	e := json.NewEncoder(app.stdout)
	if err := e.Encode(value); err != nil {
		return fmt.Errorf("writing JSON line: %w", err)
	}

	return nil
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
