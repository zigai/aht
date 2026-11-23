package cli

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/manage"
)

//exhaustruct:optional
type trackerOptions struct {
	binary   string
	interval time.Duration
	grace    time.Duration
}

func defaultInstallBinary() string {
	executable, err := os.Executable()
	if err != nil {
		return "aht"
	}

	resolved, err := filepath.EvalSymlinks(executable)
	if err == nil && resolved != "" {
		executable = resolved
	}

	absolute, err := filepath.Abs(executable)
	if err != nil {
		return executable
	}

	return absolute
}

func (app *application) manager(binary string) *manage.Manager {
	return manage.New(app.managerConfig(binary))
}

func (app *application) managerConfig(binary string) manage.Config {
	if binary == "" {
		binary = defaultInstallBinary()
	}

	return manage.Config{
		Binary:      binary,
		StorePath:   app.resolvedStorePath(),
		ConfigPath:  app.configPath,
		ConfigStdin: app.stdin,
		NoConfig:    app.noConfig,
	}
}

func (app *application) trackerManager(
	cmd *cobra.Command,
	opts trackerOptions,
) (*manage.Manager, error) {
	config := app.managerConfig(opts.binary)

	if cmd.Flags().Changed("interval") {
		if opts.interval <= 0 {
			return nil, exitCode(errInvalidObserveInterval, exitCodeUsage)
		}

		config.TrackerInterval = opts.interval
	}

	if cmd.Flags().Changed("grace-period") {
		if opts.grace < 0 {
			return nil, exitCode(errInvalidObserveGracePeriod, exitCodeUsage)
		}

		config.TrackerGracePeriod = &opts.grace
	}

	return manage.New(config), nil
}

func managerError(err error) error {
	if errors.Is(err, manage.ErrConfig) {
		return exitCode(err, exitCodeUsage)
	}

	return err
}
