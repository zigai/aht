package fakeprovidercli

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

const (
	exitCodeGeneral     = 1
	exitCodeUsage       = 2
	exitCodeInterrupted = 130
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var errSilentUsage = errors.New("")

type application struct {
	stdout     io.Writer
	outputJSON bool
}

type detail struct {
	label string
	value string
}

type usageError struct {
	err error
}

func (e usageError) Error() string { return e.err.Error() }

func (e usageError) Unwrap() error { return e.err }

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if code != 0 {
		os.Exit(code)
	}
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	app := &application{stdout: stdout, outputJSON: false}
	//nolint:contextcheck // The command tree is built before ExecuteContext receives the context.
	root := app.newRootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return exitCodeInterrupted
	}
	if err.Error() != "" {
		_, _ = fmt.Fprintln(stderr, err.Error())
	}
	var usage usageError
	if errors.As(err, &usage) || isCobraUsageError(err) {
		return exitCodeUsage
	}
	return exitCodeGeneral
}

func isCobraUsageError(err error) bool {
	message := err.Error()
	for _, prefix := range []string{"unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument", "unknown command"} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return strings.Contains(message, "accepts ") && strings.Contains(message, " arg")
}

func (app *application) newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "fakeprovider",
		Short:         "Serve a scripted fake model provider and launch coding-agent harnesses against it",
		Version:       version + " (commit: " + commit + ", built: " + date + ")",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return usageError{err: errSilentUsage}
		},
		CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
	}
	root.PersistentFlags().BoolVar(&app.outputJSON, "json", false, "emit JSON (JSON Lines for streams)")
	root.AddCommand(app.newServeCommand(), app.newStartCommand())
	return root
}

func (app *application) writeJSON(value any) error {
	if err := json.MarshalWrite(app.stdout, value, jsontext.WithIndent("  ")); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return app.writef("\n")
}

func (app *application) writeJSONLine(value any) error {
	if err := json.MarshalWrite(app.stdout, value); err != nil {
		return fmt.Errorf("write JSON line: %w", err)
	}
	return app.writef("\n")
}

func (app *application) writef(format string, args ...any) error {
	if _, err := fmt.Fprintf(app.stdout, format, args...); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func (app *application) writeDetails(details []detail) error {
	width := 0
	for _, d := range details {
		width = max(width, len(d.label)+1)
	}
	for _, d := range details {
		if err := app.writef("%-*s  %s\n", width, d.label+":", d.value); err != nil {
			return err
		}
	}
	return nil
}

func finish(runErr, cleanupErr error) error {
	switch {
	case cleanupErr == nil:
		return runErr
	case runErr == nil || errors.Is(runErr, context.Canceled):
		return cleanupErr
	default:
		return errors.Join(runErr, cleanupErr)
	}
}
