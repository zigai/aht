package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/zigai/aht/internal/brokerserver"
	"github.com/zigai/aht/internal/config"
	"github.com/zigai/aht/internal/observer"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

const (
	observeDefaultInterval = 300 * time.Millisecond
	realtimeComponentCount = 3
)

var (
	errObserverRunDegraded     = errors.New("observer reconciliation degraded")
	errUnknownServiceOperation = errors.New("unknown service operation")
)

type observeOptions struct {
	once       bool
	quiet      bool
	interval   time.Duration
	grace      time.Duration
	autoClean  bool
	maxGoneAge time.Duration
	store      goneCollector
}

type goneCollector interface {
	GC(ctx context.Context, maxAge time.Duration) (registry.GCResult, error)
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

func (app *application) newTrackerRunCommand() *cli.Command {
	o := observeOptions{interval: observeDefaultInterval}
	var autoClean bool
	screenInspection := true
	return &cli.Command{
		Name:  "run",
		Usage: "Observe agent processes and native sessions",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "once",
				Destination: &o.once,
				Usage:       "run one reconciliation cycle",
			},
			&cli.DurationFlag{
				Name:        "interval",
				Value:       o.interval,
				Destination: &o.interval,
				Usage:       "Reconciliation `duration`",
			},
			&cli.DurationFlag{
				Name:        "grace-period",
				Destination: &o.grace,
				Usage:       "Absence grace `duration`",
			},
			&cli.BoolFlag{
				Name:        "quiet",
				Aliases:     []string{"q"},
				Destination: &o.quiet,
				Usage:       "suppress human cycle output and diagnostics",
			},
			&cli.BoolFlag{
				Name:        "auto-clean",
				Destination: &autoClean,
				Usage:       "automatically clean expired gone sessions",
			},
			&cli.BoolFlag{
				Name:        "screen-inspection",
				Value:       true,
				Destination: &screenInspection,
				Usage:       "enable terminal multiplexer screen inspection",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if err := applyTrackerConfig(&o, cmd, cfg); err != nil {
				return err
			}
			if cmd.IsSet("auto-clean") {
				o.autoClean = autoClean
			}
			disableScreenInspection := cfg.Detection.ScreenInspection != nil && !*cfg.Detection.ScreenInspection
			if cmd.IsSet("screen-inspection") {
				disableScreenInspection = !screenInspection
			}
			if o.interval <= 0 {
				return exitCode(errInvalidObserveInterval, exitCodeUsage)
			}
			if o.grace < 0 {
				return exitCode(errInvalidObserveGracePeriod, exitCodeUsage)
			}
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
				o.store = app.store()
				return app.runObserver(ctx, o, watcher)
			}

			store, err := registry.OpenMemoryStore(app.resolvedStorePath())
			if err != nil {
				return fmt.Errorf("opening in-memory registry: %w", err)
			}
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
			o.store = store
			return app.runRealtimeObserver(ctx, o, watcher, store, server)
		},
	}
}

func applyTrackerConfig(o *observeOptions, cmd *cli.Command, cfg config.Config) error {
	if err := applyTrackerIntervals(o, cmd, cfg); err != nil {
		return err
	}
	if !cmd.IsSet("quiet") && cfg.Tracker.Quiet != nil {
		o.quiet = *cfg.Tracker.Quiet
	}
	applyTrackerAutoClean(o, cfg)
	return nil
}

func applyTrackerIntervals(o *observeOptions, cmd *cli.Command, cfg config.Config) error {
	if !cmd.IsSet("interval") && cfg.Tracker.Interval != "" {
		d, err := config.ParseDuration(cfg.Tracker.Interval)
		if err != nil {
			return fmt.Errorf("parsing tracker interval: %w", err)
		}
		o.interval = d
	}
	if !cmd.IsSet("grace-period") && cfg.Tracker.GracePeriod != "" {
		d, err := config.ParseDuration(cfg.Tracker.GracePeriod)
		if err != nil {
			return fmt.Errorf("parsing tracker grace period: %w", err)
		}
		o.grace = d
	}
	return nil
}

func applyTrackerAutoClean(o *observeOptions, cfg config.Config) {
	if cfg.Retention.AutoClean != nil && *cfg.Retention.AutoClean && cfg.Retention.MaxGoneAge != "" {
		d, err := config.ParseDuration(cfg.Retention.MaxGoneAge)
		if err == nil && d >= 0 {
			o.autoClean = true
			o.maxGoneAge = d
		}
	}
}

func (app *application) runRealtimeObserver(
	ctx context.Context,
	options observeOptions,
	watcher *observer.Observer,
	store *registry.MemoryStore,
	server *brokerserver.Server,
) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan trackerComponentResult, realtimeComponentCount)
	run := func(name string, operation func() error) {
		go func() {
			results <- trackerComponentResult{name: name, err: operation()}
		}()
	}
	run("broker", func() error { return server.Serve(runContext) })
	run("persistence", func() error {
		return store.RunPersistence(runContext, 0, 0)
	})
	run("observer", func() error { return app.runObserver(runContext, options, watcher) })

	first := <-results
	cancel()
	all := []trackerComponentResult{first, <-results, <-results}
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

func (app *application) runObserver(ctx context.Context, options observeOptions, watcher *observer.Observer) error {
	if options.once {
		return app.runObserverOnce(ctx, options, watcher)
	}
	if !options.quiet {
		app.warnf("observer started interval=%s grace-period=%s\n", options.interval, options.grace)
	}
	store := options.store
	if store == nil {
		store = app.store()
	}
	handle := func(result observer.Result) error {
		if options.autoClean {
			if _, err := store.GC(ctx, options.maxGoneAge); err != nil {
				return fmt.Errorf("clean gone sessions: %w", err)
			}
		}
		if app.outputJSON {
			return app.writeJSONLine(result)
		}
		if options.quiet {
			return nil
		}
		return app.writeObserverResult(result)
	}
	var err error
	if options.quiet && !app.outputJSON && !options.autoClean {
		err = watcher.Run(ctx)
	} else {
		err = watcher.RunWithResults(ctx, handle)
	}
	if err != nil {
		return fmt.Errorf("observer run: %w", err)
	}
	return nil
}

func (app *application) runObserverOnce(ctx context.Context, options observeOptions, watcher *observer.Observer) error {
	result, err := watcher.RunOnce(ctx)
	if err != nil {
		return fmt.Errorf("observer run once: %w", err)
	}
	if options.autoClean {
		store := options.store
		if store == nil {
			store = app.store()
		}
		if _, err := store.GC(ctx, options.maxGoneAge); err != nil {
			return fmt.Errorf("clean gone sessions: %w", err)
		}
	}
	var writeErr error
	if app.outputJSON {
		writeErr = app.writeJSON(result)
	} else if !options.quiet {
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

func runServiceOperation(ctx context.Context, operation string, options service.Options) (service.Result, error) {
	var result service.Result
	var err error
	switch operation {
	case "update":
		result, err = service.Update(ctx, options)
	case "uninstall":
		result, err = service.Uninstall(ctx, options)
	case statusCommandName:
		result, err = service.Status(ctx, options)
	default:
		return service.Result{}, fmt.Errorf("%w: %s", errUnknownServiceOperation, operation)
	}
	if err != nil {
		return result, fmt.Errorf("service %s: %w", operation, err)
	}
	return result, nil
}

func (app *application) parseServiceOptions(options serviceOptions) (service.Options, error) {
	if options.interval <= 0 {
		return service.Options{}, errInvalidObserveInterval
	}
	if options.grace < 0 {
		return service.Options{}, errInvalidObserveGracePeriod
	}
	return service.Options{Binary: options.binary, StorePath: app.resolvedStorePath(), Interval: options.interval, GracePeriod: options.grace, DryRun: options.dryRun}, nil
}

func (app *application) configuredServiceOptions(cmd *cli.Command, options serviceOptions) (service.Options, error) {
	cfg, err := app.loadConfig()
	if err != nil {
		return service.Options{}, err
	}
	intervals := observeOptions{interval: options.interval, grace: options.grace}
	if err := applyTrackerIntervals(&intervals, cmd, cfg); err != nil {
		return service.Options{}, err
	}
	options.interval, options.grace = intervals.interval, intervals.grace
	return app.parseServiceOptions(options)
}
