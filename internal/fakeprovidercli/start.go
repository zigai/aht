package fakeprovidercli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	rootStamp        = "20060102-150405.000"
	exitPollInterval = 250 * time.Millisecond
	shutdownTimeout  = 10 * time.Second
	terminalWidth    = 200
	terminalHeight   = 50
)

var errIntegrationConflict = errors.New("--aht and --no-integration cannot be combined")

type startOptions struct {
	harness       registry.Harness
	root          string
	prompt        string
	aht           string
	noIntegration bool
}

type startRun struct {
	provider *fakeprovider.Server
	launched *launch.Harness
	term     *launch.Terminal
}

type startInfo struct {
	Harness     registry.Harness `json:"harness"`
	Root        string           `json:"root"`
	Store       string           `json:"store"`
	ProviderURL string           `json:"provider_url"`
	Attach      []string         `json:"attach"`
	Inspect     []string         `json:"inspect,omitempty"`
}

func (app *application) newStartCommand() *cobra.Command {
	var options startOptions

	command := &cobra.Command{
		Use:   "start <harness>",
		Short: "Start a harness against the fake model provider",
		Long: "Start the harness in a private tmux server with its own root and the Echo fake model provider. " +
			"By default the AHT integration and tracker from the aht executable on PATH observe the session. " +
			"Attach with the printed command. The command stays in the foreground until Ctrl-C or the harness exits. " +
			"The root stays on disk.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			harnessID, err := launchableHarness(args[0])
			if err != nil {
				return err
			}

			if options.aht, err = resolveAHT(options.aht, options.noIntegration); err != nil {
				return err
			}

			if options.root, err = rootDirectory(options.root, harnessID, time.Now()); err != nil {
				return err
			}

			options.harness = harnessID

			return app.runStart(cmd.Context(), options)
		},
	}
	flags := command.Flags()
	flags.StringVarP(
		&options.root,
		"root",
		"r",
		"",
		"launch root `<dir>` [default: new directory under the user cache directory]",
	)
	flags.StringVarP(&options.prompt, "prompt", "p", "", "initial prompt `<text>`")
	flags.StringVar(
		&options.aht,
		"aht",
		"",
		"aht executable `<path>` for the integration and tracker [default: aht on PATH]",
	)
	flags.BoolVar(
		&options.noIntegration,
		"no-integration",
		false,
		"skip the AHT integration and tracker",
	)

	return command
}

func (app *application) runStart(ctx context.Context, options startOptions) error {
	//nolint:contextcheck // The provider serves until Close, so its lifetime is not bound to the command context.
	provider, err := fakeprovider.Start(fakeprovider.Options{
		Address:     "",
		Model:       "",
		Script:      fakeprovider.Echo(),
		Hold:        false,
		SideMarkers: nil,
	})
	if err != nil {
		return fmt.Errorf("start fake provider: %w", err)
	}

	launched, term, err := startHarness(ctx, provider.URL(), options)
	if err != nil {
		return finish(err, provider.Close())
	}

	run := startRun{provider: provider, launched: launched, term: term}
	if err := app.writeStart(run, options); err != nil {
		return finish(err, run.close(ctx))
	}

	return finish(waitForExit(ctx, term), run.close(ctx))
}

func startHarness(
	ctx context.Context,
	providerURL string,
	options startOptions,
) (*launch.Harness, *launch.Terminal, error) {
	launched, err := launch.New(ctx, launch.Options{
		Harness:    options.harness,
		Root:       options.root,
		Endpoint:   providerURL,
		Model:      "",
		APIKey:     "",
		AHT:        options.aht,
		Executable: "",
		Env:        nil,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("prepare launch: %w", err)
	}

	if options.aht != "" {
		if err := launched.StartTracker(ctx); err != nil {
			return nil, nil, finish(fmt.Errorf("start tracker: %w", err), launched.Close())
		}
	}

	term, err := launched.StartTerminal(
		ctx,
		options.prompt,
		gotmux.Size{Width: terminalWidth, Height: terminalHeight},
	)
	if err != nil {
		return nil, nil, finish(fmt.Errorf("start terminal: %w", err), launched.Close())
	}

	return launched, term, nil
}

func (run startRun) close(parent context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), shutdownTimeout)
	defer cancel()

	return errors.Join(run.term.Close(ctx), run.launched.Close(), run.provider.Close())
}

func waitForExit(ctx context.Context, term *launch.Terminal) error {
	ticker := time.NewTicker(exitPollInterval)
	defer ticker.Stop()

	for {
		exited, err := term.Exited(ctx)
		if ctx.Err() != nil {
			return fmt.Errorf("wait for harness exit: %w", ctx.Err())
		}

		if err != nil {
			return fmt.Errorf("check harness exit: %w", err)
		}

		if exited {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for harness exit: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (app *application) writeStart(run startRun, options startOptions) error {
	info := startInfo{
		Harness:     options.harness,
		Root:        run.launched.Root,
		Store:       run.launched.Store,
		ProviderURL: run.provider.URL(),
		Attach:      run.term.AttachCommand(),
		Inspect:     nil,
	}
	if options.aht != "" {
		info.Inspect = []string{
			options.aht,
			"--store",
			run.launched.Store,
			"list",
			"--cwd",
			run.launched.Work,
		}
	}

	if app.outputJSON {
		return app.writeJSON(info)
	}

	details := []detail{
		{label: "Harness", value: string(info.Harness)},
		{label: "Root", value: info.Root},
		{label: "Provider", value: info.ProviderURL},
		{label: "Attach", value: shellCommand(info.Attach)},
	}
	if info.Inspect != nil {
		details = append(details, detail{label: "Inspect", value: shellCommand(info.Inspect)})
	}

	return app.writeDetails(details)
}

func shellCommand(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, word := range argv {
		quoted = append(quoted, harness.ShellQuote(word))
	}

	return strings.Join(quoted, " ")
}

func launchableHarness(value string) (registry.Harness, error) {
	harnessID, err := catalog.Parse(value)
	if err == nil {
		adapter, _ := catalog.Find(harnessID)
		if _, ok := adapter.(harness.Launchable); ok {
			return harnessID, nil
		}

		err = fmt.Errorf("%w: %q", launch.ErrUnsupported, value)
	}

	return "", usageError{
		err: fmt.Errorf(
			"%w; supported harnesses: %s",
			err,
			strings.Join(launchableHarnessNames(), ", "),
		),
	}
}

func launchableHarnessNames() []string {
	var names []string

	for _, adapter := range catalog.All() {
		if _, ok := adapter.(harness.Launchable); ok {
			names = append(names, string(adapter.Definition().ID))
		}
	}

	slices.Sort(names)

	return names
}

func resolveAHT(path string, noIntegration bool) (string, error) {
	switch {
	case noIntegration && path != "":
		return "", usageError{err: errIntegrationConflict}
	case noIntegration:
		return "", nil
	case path == "":
		path = "aht"
	}

	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", usageError{
			err: fmt.Errorf("find aht executable (pass --aht <path> or --no-integration): %w", err),
		}
	}

	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve aht executable: %w", err)
	}

	return absolute, nil
}

func rootDirectory(root string, harnessID registry.Harness, now time.Time) (string, error) {
	if root != "" {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve launch root: %w", err)
		}

		return absolute, nil
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}

	return filepath.Join(
		cache,
		"aht",
		"fakeprovider",
		string(harnessID)+"-"+now.Format(rootStamp),
	), nil
}
