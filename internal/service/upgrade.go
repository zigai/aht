package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

var errInstalledArguments = errors.New("cannot recover installed tracker settings")

// Upgrade refreshes an existing definition, preserving its stored options and
// running state. Missing services remain uninstalled.
func Upgrade(ctx context.Context, binary string, dryRun bool) (Result, error) {
	return defaultService.Upgrade(ctx, binary, dryRun)
}

func (s *Service) Upgrade(ctx context.Context, binary string, dryRun bool) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("upgrade service: %w", err)
	}

	path, err := definitionPath()
	if err != nil {
		return Result{}, err
	}

	result := describeDefinition(path)

	previous, err := os.ReadFile(result.ManagedPath)
	if errors.Is(err, os.ErrNotExist) {
		result.Message = "not installed; skipped"
		return result, nil
	}

	if err != nil {
		return result, fmt.Errorf("read managed service: %w", err)
	}

	if !isManaged(string(previous)) {
		return result, fmt.Errorf("%w: %s", ErrForeign, result.ManagedPath)
	}

	result.Installed = true

	b, err := recoveredBackend(previous, binary)
	if err != nil {
		return result, err
	}

	result.Current = string(previous) == b.content()

	state, err := b.running(ctx, s.executor)
	if err != nil {
		return result, err
	}

	result.Running = state.Running
	restart := state.Running || (state.Loaded && !result.Current)

	if dryRun {
		result.Message = "would update (stopped)"
		if restart {
			result.Message = "would update and restart"
		}

		return result, nil
	}

	return s.upgradeDefinition(ctx, b, result, previous, restart)
}

func (s *Service) upgradeDefinition(
	ctx context.Context,
	b backend,
	result Result,
	previous []byte,
	restart bool,
) (Result, error) {
	if !result.Current {
		if err := s.refreshUpgradeDefinition(ctx, b, result, restart); err != nil {
			return result, s.rollbackUpgrade(ctx, b, result, previous, restart, err)
		}
	}

	if restart {
		if err := b.restart(ctx, s.executor); err != nil {
			return result, s.rollbackUpgrade(ctx, b, result, previous, restart, err)
		}
	}

	result.Changed = !result.Current || restart
	result.Current = true

	result.Message = "stopped (up to date)"
	if result.Changed {
		result.Message = "stopped (updated)"
	}

	if restart {
		result.Message = "updated and restarted"
	}

	return result, nil
}

func (s *Service) refreshUpgradeDefinition(
	ctx context.Context,
	b backend,
	result Result,
	restart bool,
) error {
	if err := writeAtomic(result.ManagedPath, []byte(b.content())); err != nil {
		return err
	}

	if !restart {
		if err := b.reload(ctx, s.executor); err != nil {
			return fmt.Errorf("reload upgraded tracker: %w", err)
		}
	}

	return nil
}

func (s *Service) rollbackUpgrade(
	ctx context.Context,
	b backend,
	result Result,
	previous []byte,
	restart bool,
	cause error,
) error {
	// Upgrade restores the previous manager state: stopped systemd units stay
	// stopped, while loaded launchd jobs must load the restored definition.
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceRollbackTime)
	defer cancel()

	if err := writeAtomic(result.ManagedPath, previous); err != nil {
		return errors.Join(cause, fmt.Errorf("restore service definition: %w", err))
	}

	failures := []error{cause}
	if restart {
		failures = append(failures, b.restart(rollbackCtx, s.executor))
	} else {
		failures = append(failures, b.reload(rollbackCtx, s.executor))
	}

	return errors.Join(failures...)
}

func recoverOptions(args []string, binary string) (Options, error) {
	options := Options{Binary: binary, StorePath: "", Interval: 0, GracePeriod: 0, DryRun: false}

	for i := 1; i < len(args); i++ {
		flag := args[i]
		switch flag {
		case "--store", "--interval", "--grace-period":
			i++
			if i >= len(args) {
				return options, errInstalledArguments
			}

			if flag == "--store" {
				options.StorePath = args[i]
				continue
			}

			value, err := time.ParseDuration(args[i])
			if err != nil {
				return options, fmt.Errorf("%w: %s: %w", errInstalledArguments, flag, err)
			}

			if flag == "--interval" {
				options.Interval = value
			} else {
				options.GracePeriod = value
			}
		case "manage", "tracker", "run", "--quiet":
		default:
			return options, fmt.Errorf("%w: unsupported argument %q", errInstalledArguments, flag)
		}
	}

	if options.StorePath == "" || options.Interval <= 0 {
		return options, errInstalledArguments
	}

	return options, nil
}

func recoveredBackend(previous []byte, binary string) (backend, error) {
	args, err := installedArguments(previous)
	if err != nil {
		return nil, err
	}

	options, err := recoverOptions(args, binary)
	if err != nil {
		return nil, err
	}

	b, err := platformBackend(options)
	if err != nil {
		return nil, err
	}

	return b, nil
}
