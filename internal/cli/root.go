package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/cobrausage"
	"github.com/zigai/aht/v2/internal/config"

	"github.com/zigai/aht/v2/internal/processinfo"

	"github.com/zigai/aht/v2/pkg/client"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	registryIDShortLength = 8
	listCommandName       = "list"
	statusCommandName     = "status"
	installCommandName    = "install"
	sigpipeExitCode       = 141
	exitCodeGeneral       = 1
	exitCodeUsage         = 2
	exitCodeInterrupted   = 130
	integrationsCommand   = "integrations"
	trackerCommand        = "tracker"
	stateCommandName      = "state"
	hookCommandName       = "hook"
	hoursPerDay           = 24
	jsonIndent            = "  "
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	dirty   = ""

	errUnknownCommand       = errors.New("unknown command")
	errUnknownHelpShorthand = errors.New("unknown shorthand flag: 'h' in -h")
	errSilentUsageError     = errors.New("")

	configureCobraOnce sync.Once
)

type application struct {
	//exhaustruct:optional
	storePath string
	//exhaustruct:optional
	configPath string
	//exhaustruct:optional
	noConfig bool
	//exhaustruct:optional
	cfgLoaded bool
	//exhaustruct:optional
	cfg config.Config
	//exhaustruct:optional
	cfgMeta *strata.Metadata
	//exhaustruct:optional
	resolvedConfigPath string
	//exhaustruct:optional
	cfgErr error
	//exhaustruct:optional
	outputJSON bool
	//exhaustruct:optional
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

type exitCoderError struct {
	err  error
	code int
}

func (e *exitCoderError) Error() string {
	return e.err.Error()
}

func (e *exitCoderError) Unwrap() error {
	return e.err
}

func (e *exitCoderError) ExitCode() int {
	return e.code
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := executeCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)

	stop()

	if code != 0 {
		os.Exit(code)
	}
}

func exitCode(err error, code int) error {
	if err == nil {
		return nil
	}

	return &exitCoderError{err: err, code: code}
}

func (app *application) configureCommandTree(cmd *cobra.Command) {
	if f := cmd.Flags().Lookup("help"); f != nil {
		f.Shorthand = ""
	} else {
		cmd.Flags().Bool("help", false, "show help")
	}

	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if errors.Is(err, pflag.ErrHelp) ||
			strings.Contains(err.Error(), "unknown shorthand flag: 'h'") {
			return exitCode(errUnknownHelpShorthand, exitCodeUsage)
		}

		return err
	})

	for _, sub := range cmd.Commands() {
		app.configureCommandTree(sub)
	}
}

func configureCobra() {
	configureCobraOnce.Do(func() {
		cobra.EnableCommandSorting = false
	})
}

func (app *application) newRootCommand() *cobra.Command {
	configureCobra()

	var showVersion bool

	root := &cobra.Command{
		Use:           "aht",
		Short:         "Discover, track, and search local coding-agent sessions",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				if app.outputJSON {
					return app.writeJSON(map[string]string{
						"version": version,
						"commit":  commit,
						"built":   date,
					})
				}

				return app.writef("%s\n", versionText())
			}

			if len(args) > 0 {
				return exitCode(
					fmt.Errorf("%w %q for %q", errUnknownCommand, args[0], cmd.Name()),
					exitCodeUsage,
				)
			}

			_ = cmd.Help()

			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	root.SetOut(app.stdout)
	root.SetErr(app.stderr)

	if app.stdin != nil {
		root.SetIn(app.stdin)
	}

	root.PersistentFlags().StringVar(&app.storePath, "store", "", "registry state `<path>`")
	root.PersistentFlags().StringVar(&app.configPath, "config", "", "config file `<path>`")
	root.PersistentFlags().
		BoolVar(&app.noConfig, "no-config", false, "bypass all configuration files")
	root.PersistentFlags().
		BoolVar(&app.outputJSON, "json", false, "emit JSON (JSON Lines for streams)")
	root.PersistentFlags().Bool("help", false, "show help")
	root.Flags().BoolVarP(&showVersion, "version", "V", false, "print version")

	root.AddCommand(
		app.newListCommand(),
		app.newSearchCommand(),
		app.newWatchCommand(),
		app.newWaitCommand(),
		app.newInfoCommand(),
		app.newCurrentCommand(),
		app.newStopCommand(),
		app.newManageCommand(),
		app.newHookCommand(),
		app.newReportCommand(),
	)
	// Cobra appends its generated commands; doctor sits between completion and help.
	root.InitDefaultCompletionCmd()
	root.AddCommand(app.newDoctorCommand())
	root.InitDefaultHelpCmd()
	app.configureCommandTree(root)

	return root
}

func (app *application) newManageCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "manage",
		Short:         "Manage setup, integrations, tracking, and state",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(
		app.newSetupCommand(),
		app.newUpgradeCommand(),
		app.newIntegrationsCommand(),
		app.newTrackerCommand(),
		app.newStateCommand(),
		app.newCapabilitiesCommand(),
		app.newLocationsCommand(),
		app.newManageConfigCommand(),
		app.newDetectionCommand(),
		app.newHistoryCommand(),
	)

	return command
}

func executeCLI(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) int {
	app := &application{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
	//nolint:contextcheck // Command tree is built before ExecuteContext receives the context.
	root := app.newRootCommand()
	root.SetArgs(args)

	if stdin != nil {
		root.SetIn(stdin)
	}

	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		return app.handleError(err)
	}

	return 0
}

func (app *application) handleError(err error) int {
	if errors.Is(err, syscall.EPIPE) {
		return sigpipeExitCode
	}

	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		code := exitCoder.ExitCode()
		if code == sigpipeExitCode {
			return sigpipeExitCode
		}

		if errors.Is(err, context.Canceled) || code == exitCodeInterrupted {
			return exitCodeInterrupted
		}

		app.printError(err)

		return code
	}

	if errors.Is(err, context.Canceled) {
		return exitCodeInterrupted
	}

	app.printError(err)

	if cobrausage.Is(err) {
		return exitCodeUsage
	}

	return exitCodeGeneral
}

func (app *application) printError(err error) {
	if err.Error() == "" {
		return
	}

	_, _ = fmt.Fprintln(app.stderr, err.Error())
	if app.environmentError(err) {
		_, _ = fmt.Fprintln(app.stderr, "Run 'aht doctor' for details.")
	}
}

func (app *application) environmentError(err error) bool {
	if errors.Is(err, config.ErrConfigNotFound) {
		return false
	}

	if app.cfgErr != nil && errors.Is(err, app.cfgErr) {
		return true
	}

	if _, ok := errors.AsType[*registry.UnsupportedSchemaError](err); ok {
		return true
	}

	if _, ok := errors.AsType[*processinfo.PermissionError](err); ok {
		return true
	}

	return errors.Is(err, registry.ErrCorruptStore)
}

func (app *application) resolvedStorePath() string {
	if app.storePath != "" {
		return app.storePath
	}

	return registry.DefaultStorePath()
}

func (app *application) registryStore() *client.Client {
	return client.New(client.Config{StorePath: app.resolvedStorePath()})
}
