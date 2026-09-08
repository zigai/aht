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
	b, err := platformBackend(Options{Binary: binary, StorePath: "/", Interval: 0, GracePeriod: 0, DryRun: dryRun})
	if err != nil {
		return Result{}, err
	}
	result := b.describe()
	previous, err := os.ReadFile(result.Path)
	if errors.Is(err, os.ErrNotExist) {
		result.Message = "not installed; skipped"
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read managed service: %w", err)
	}
	if !isManaged(string(previous)) {
		return result, fmt.Errorf("%w: %s", ErrForeign, result.Path)
	}
	result.Installed = true
	args, err := installedArguments(previous)
	if err != nil {
		return result, err
	}
	options, err := recoverOptions(args, binary)
	if err != nil {
		return result, err
	}
	b, err = platformBackend(options)
	if err != nil {
		return result, err
	}
	result.Current = string(previous) == b.content()
	result.Running, _, err = b.running(ctx, s.executor)
	if err != nil {
		return result, err
	}
	if dryRun {
		result.Message = "would update (stopped)"
		if result.Running {
			result.Message = "would update and restart"
		}
		return result, nil
	}
	return s.upgradeDefinition(ctx, b, result, previous)
}

func (s *Service) upgradeDefinition(ctx context.Context, b backend, result Result, previous []byte) (Result, error) {
	if !result.Current {
		if err := s.refreshUpgradeDefinition(ctx, b, result); err != nil {
			return result, s.rollbackUpgrade(ctx, b, result, previous, err)
		}
	}
	if result.Running {
		if err := b.restart(ctx, s.executor); err != nil {
			return result, s.rollbackUpgrade(ctx, b, result, previous, err)
		}
	}
	result.Changed = !result.Current || result.Running
	result.Current = true
	result.Message = "stopped (up to date)"
	if result.Changed {
		result.Message = "stopped (updated)"
	}
	if result.Running {
		result.Message = "updated and restarted"
	}
	return result, nil
}

func (s *Service) refreshUpgradeDefinition(ctx context.Context, b backend, result Result) error {
	if err := writeAtomic(result.Path, []byte(b.content())); err != nil {
		return err
	}
	// launchd caches loaded definitions. Unload a stopped job so its next
	// bootstrap reads the refreshed plist without starting it now.
	if result.Platform == "darwin" && !result.Running {
		if err := b.unload(ctx, s.executor); err != nil {
			return fmt.Errorf("unload stopped tracker: %w", err)
		}
	}
	if err := b.reload(ctx, s.executor); err != nil {
		return fmt.Errorf("reload upgraded tracker: %w", err)
	}
	return nil
}

func (s *Service) rollbackUpgrade(ctx context.Context, b backend, result Result, previous []byte, cause error) error {
	// The normal install rollback starts the old service. An upgrade must never
	// start a service that was stopped, including on a failed file/manager update.
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceRollbackTime)
	defer cancel()
	if err := writeAtomic(result.Path, previous); err != nil {
		return errors.Join(cause, fmt.Errorf("restore service definition: %w", err))
	}
	failures := []error{cause, b.reload(rollbackCtx, s.executor)}
	if result.Running {
		failures = append(failures, b.restart(rollbackCtx, s.executor))
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
		case "manage", "tracker", "run", "observe", "monitor", "--quiet":
		default:
			return options, fmt.Errorf("%w: unsupported argument %q", errInstalledArguments, flag)
		}
	}
	if options.StorePath == "" || options.Interval <= 0 {
		return options, errInstalledArguments
	}
	return options, nil
}
