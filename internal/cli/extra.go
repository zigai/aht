package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/brokerserver"
	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/observer"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	observeDefaultInterval = 300 * time.Millisecond
	realtimeComponentCount = 4
)

var (
	errObserverRunDegraded     = errors.New("observer reconciliation degraded")
	errUnknownServiceOperation = errors.New("unknown service operation")
)

type observeOptions struct {
	once         bool
	quiet        bool
	interval     time.Duration
	grace        time.Duration
	tombstoneTTL time.Duration
}

type trackerComponentResult struct {
	name string
	err  error
}

type serviceOptions struct {
	binary          string
	interval, grace time.Duration
	dryRun          bool
}

func (app *application) newTrackerRunCommand() *cobra.Command {
	o := observeOptions{interval: observeDefaultInterval}
	screenInspection := true
	var disableScreenInspection bool
	command := &cobra.Command{
		Use:           "run",
		Short:         "Observe agent processes and native sessions",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if err := applyTrackerConfig(&o, cmd, cfg); err != nil {
				return err
			}
			disableScreenInspection = cfg.Detection.ScreenInspection != nil && !*cfg.Detection.ScreenInspection
			if cmd.Flags().Changed("screen-inspection") {
				disableScreenInspection = !screenInspection
			}
			if o.interval <= 0 {
				return exitCode(errInvalidObserveInterval, exitCodeUsage)
			}
			if o.grace < 0 {
				return exitCode(errInvalidObserveGracePeriod, exitCodeUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := app.cfg
			if o.once {
				watcher := observer.New(observer.Options{
					StorePath:               app.resolvedStorePath(),
					Interval:                o.interval,
					GracePeriod:             o.grace,
					HealthPath:              app.resolvedStorePath() + ".observer-health.json",
					Quiet:                   o.quiet,
					DetectionConfigDir:      cfg.Detection.ManifestsDir,
					DisableScreenInspection: disableScreenInspection,
				})
				return app.runObserver(cmd.Context(), o, watcher)
			}

			store, err := registry.OpenMemoryStoreWithOptions(app.resolvedStorePath(), catalog.Rules{}, registry.MemoryStoreOptions{TombstoneTTL: o.tombstoneTTL})
			if err != nil {
				return fmt.Errorf("opening in-memory registry: %w", err)
			}
			defer func() {
				if err := store.Close(); err != nil {
					app.warnf("warning: %v\n", err)
				}
			}()
			watcher := observer.New(observer.Options{
				Store:                   store,
				StorePath:               store.Path(),
				Interval:                o.interval,
				GracePeriod:             o.grace,
				HealthPath:              store.Path() + ".observer-health.json",
				Quiet:                   o.quiet,
				DetectionConfigDir:      cfg.Detection.ManifestsDir,
				DisableScreenInspection: disableScreenInspection,
			})
			server := brokerserver.New(brokerserver.Options{
				Store:      store,
				SocketPath: broker.SocketPath(store.Path()),
			})
			return app.runRealtimeObserver(cmd.Context(), o, watcher, store, server)
		},
	}
	flags := command.Flags()
	flags.BoolVar(&o.once, "once", false, "run one reconciliation cycle")
	flags.DurationVar(&o.interval, "interval", o.interval, "reconciliation `<duration>`")
	flags.DurationVar(&o.grace, "grace-period", o.grace, "absence grace `<duration>`")
	flags.BoolVarP(&o.quiet, "quiet", "q", false, "suppress human cycle output and diagnostics")
	flags.BoolVar(&screenInspection, "screen-inspection", true, "enable terminal multiplexer screen inspection")
	return command
}

func applyTrackerConfig(opts *observeOptions, cmd *cobra.Command, cfg config.Config) error {
	if err := applyTrackerIntervals(opts, cmd, cfg); err != nil {
		return err
	}
	if !cmd.Flags().Changed("quiet") && cfg.Tracker.Quiet != nil {
		opts.quiet = *cfg.Tracker.Quiet
	}
	ttl, err := config.TombstoneTTL(cfg)
	if err != nil {
		return exitCode(fmt.Errorf("parsing retention tombstone TTL: %w", err), exitCodeUsage)
	}
	opts.tombstoneTTL = ttl
	return nil
}

func applyTrackerIntervals(opts *observeOptions, cmd *cobra.Command, cfg config.Config) error {
	if !cmd.Flags().Changed("interval") && cfg.Tracker.Interval != "" {
		d, err := config.ParseDuration(cfg.Tracker.Interval)
		if err != nil {
			return exitCode(fmt.Errorf("parsing tracker interval: %w", err), exitCodeUsage)
		}
		opts.interval = d
	}
	if !cmd.Flags().Changed("grace-period") && cfg.Tracker.GracePeriod != "" {
		d, err := config.ParseDuration(cfg.Tracker.GracePeriod)
		if err != nil {
			return exitCode(fmt.Errorf("parsing tracker grace period: %w", err), exitCodeUsage)
		}
		opts.grace = d
	}
	return nil
}

func (app *application) runRealtimeObserver(
	ctx context.Context,
	opts observeOptions,
	watcher *observer.Observer,
	store *registry.MemoryStore,
	server *brokerserver.Server,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan trackerComponentResult, realtimeComponentCount)
	run := func(name string, operation func() error) {
		go func() {
			results <- trackerComponentResult{name: name, err: operation()}
		}()
	}
	run("broker", func() error { return server.Serve(runCtx) })
	run("persistence", func() error {
		return store.RunPersistence(runCtx, 0, 0)
	})
	run("observer", func() error { return app.runObserver(runCtx, opts, watcher) })
	run("history", func() error { return app.runHistoryIndexer(runCtx, store, opts.quiet) })

	first := <-results
	cancel()
	all := []trackerComponentResult{first, <-results, <-results, <-results}
	var joined error
	for _, result := range all {
		if result.err == nil {
			continue
		}
		joined = errors.Join(joined, fmt.Errorf("%s: %w", result.name, result.err))
	}
	if joined != nil {
		return joined
	}

	return nil
}

func (app *application) runObserver(ctx context.Context, opts observeOptions, watcher *observer.Observer) error {
	if opts.once {
		return app.runObserverOnce(ctx, opts, watcher)
	}
	if !opts.quiet {
		app.warnf("observer started interval=%s grace-period=%s\n", opts.interval, opts.grace)
	}
	handle := func(result observer.Result) error {
		if app.outputJSON {
			return app.writeJSONLine(result)
		}
		if opts.quiet {
			return nil
		}
		return app.writeObserverResult(result)
	}
	var err error
	if opts.quiet && !app.outputJSON {
		err = watcher.Run(ctx)
	} else {
		err = watcher.RunWithResults(ctx, handle)
	}
	if err != nil {
		return fmt.Errorf("observer run: %w", err)
	}
	return nil
}

func (app *application) runObserverOnce(ctx context.Context, opts observeOptions, watcher *observer.Observer) error {
	result, err := watcher.RunOnce(ctx)
	if err != nil {
		return fmt.Errorf("observer run once: %w", err)
	}
	// A running tracker expires tombstones continuously; a single cycle has
	// to do it explicitly or its fallback writes would keep them forever.
	ttl := opts.tombstoneTTL
	if ttl <= 0 {
		ttl = registry.DefaultTombstoneTTL
	}
	if _, err := app.registryStore().GC(ctx, ttl); err != nil {
		return fmt.Errorf("expiring tombstones: %w", err)
	}
	var writeErr error
	if app.outputJSON {
		writeErr = app.writeJSON(result)
	} else if !opts.quiet {
		writeErr = app.writeObserverResult(result)
	}
	if writeErr != nil {
		return writeErr
	}
	if !result.Degraded {
		return nil
	}
	if result.Error == "" {
		return errObserverRunDegraded
	}
	return fmt.Errorf("%w: %s", errObserverRunDegraded, result.Error)
}

func (app *application) writeObserverResult(result observer.Result) error {
	if err := app.writef(
		"observations=%d sessions=%d processes=%d panes=%d catalog=%d\n",
		result.Observations, result.Sessions, result.Processes, result.Panes, result.Catalog,
	); err != nil {
		return err
	}
	if err := app.writef(
		"present=%d gone=%d changed=%d degraded=%t\n",
		result.Present, result.Gone, result.Changed, result.Degraded,
	); err != nil {
		return err
	}
	if result.Error != "" {
		return app.writeHumanDetails([]humanDetail{{label: "Error", value: result.Error}})
	}
	return nil
}

func runServiceOperation(ctx context.Context, operation string, opts service.Options) (service.Result, error) {
	var result service.Result
	var err error
	switch operation {
	case "update":
		result, err = service.Update(ctx, opts)
	case "uninstall":
		result, err = service.Uninstall(ctx, opts)
	case statusCommandName:
		result, err = service.Status(ctx, opts)
	default:
		return service.Result{}, fmt.Errorf("%w: %s", errUnknownServiceOperation, operation)
	}
	if err != nil {
		return result, fmt.Errorf("service %s: %w", operation, err)
	}
	return result, nil
}

func (app *application) parseServiceOptions(opts serviceOptions) (service.Options, error) {
	if opts.binary == "" {
		opts.binary = defaultInstallBinary()
	}
	if opts.interval <= 0 {
		return service.Options{}, exitCode(errInvalidObserveInterval, exitCodeUsage)
	}
	if opts.grace < 0 {
		return service.Options{}, exitCode(errInvalidObserveGracePeriod, exitCodeUsage)
	}
	return service.Options{Binary: opts.binary, StorePath: app.resolvedStorePath(), Interval: opts.interval, GracePeriod: opts.grace, DryRun: opts.dryRun}, nil
}

func (app *application) configuredServiceOptions(cmd *cobra.Command, opts serviceOptions) (service.Options, error) {
	cfg, err := app.loadConfig()
	if err != nil {
		return service.Options{}, err
	}
	intervals := observeOptions{interval: opts.interval, grace: opts.grace}
	if err := applyTrackerIntervals(&intervals, cmd, cfg); err != nil {
		return service.Options{}, err
	}
	opts.interval, opts.grace = intervals.interval, intervals.grace
	return app.parseServiceOptions(opts)
}
