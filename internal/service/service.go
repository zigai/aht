package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/command"
)

const (
	ManagedVersion            = 8
	ManagedMarker             = "aht managed observer service"
	serviceDirectoryMode      = 0o755
	serviceRollbackTime       = 10 * time.Second
	serviceCommandTimeout     = 30 * time.Second
	serviceCommandOutputLimit = 4 << 20
)

var (
	ErrForeign           = errors.New("service path contains foreign content")
	errBinaryRequired    = errors.New("binary is required")
	errStorePathRequired = errors.New("store path is required")
	errIntervalPositive  = errors.New("interval must be positive")
	errGraceNonnegative  = errors.New("grace period must be nonnegative")
	defaultService       = New(nil)
)

// Options controls the managed observer service.
type Options struct {
	Binary      string
	StorePath   string
	Interval    time.Duration
	GracePeriod time.Duration
	DryRun      bool
}

// Result describes the service state after an operation.
type Result struct {
	Platform       string `json:"platform"`
	Manager        string `json:"manager"`
	ManagedPath    string `json:"managed_path"`
	ManagedVersion int    `json:"managed_version"`
	Installed      bool   `json:"installed"`
	Current        bool   `json:"current"`
	Running        bool   `json:"running"`
	Changed        bool   `json:"changed"`
	Message        string `json:"message"`
}

// CommandExecutor runs a manager command without invoking a shell.
type CommandExecutor interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Service manages the platform-native observer service.
type Service struct {
	executor CommandExecutor
}

type osCommandExecutor struct{}

func (osCommandExecutor) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := command.RunWithLimits(
		ctx,
		serviceCommandTimeout,
		serviceCommandOutputLimit,
		name,
		nil,
		args...,
	)
	if err != nil {
		return output, fmt.Errorf("execute service manager: %w", err)
	}

	return output, nil
}

// New constructs a service using an injected command executor.
func New(executor CommandExecutor) *Service {
	if executor == nil {
		return &Service{executor: osCommandExecutor{}}
	}

	return &Service{executor: executor}
}

func Update(ctx context.Context, opts Options) (Result, error) {
	return defaultService.Update(ctx, opts)
}

func Uninstall(ctx context.Context, dryRun bool) (Result, error) {
	return defaultService.Uninstall(ctx, dryRun)
}

func Status(ctx context.Context, opts Options) (Result, error) {
	return defaultService.Status(ctx, opts)
}

func (s *Service) Uninstall(ctx context.Context, dryRun bool) (Result, error) {
	backend, err := definitionBackend("")
	if err != nil {
		return Result{}, err
	}

	result := backend.describe()

	content, readErr := os.ReadFile(result.ManagedPath)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			result.Message = "not installed"
			return result, nil
		}

		return result, fmt.Errorf("read managed service: %w", readErr)
	}

	if !isManaged(string(content)) {
		return result, fmt.Errorf("%w: %s", ErrForeign, result.ManagedPath)
	}

	result.Installed = true
	if dryRun {
		result.Message = "would uninstall"
		return result, nil
	}

	if err := backend.unload(ctx, s.executor); err != nil {
		return result, err
	}

	if err := os.Remove(result.ManagedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		cause := fmt.Errorf("remove managed service: %w", err)

		return result, s.rollbackDefinition(ctx, backend, result.ManagedPath, content, true, cause)
	}

	if err := backend.reload(ctx, s.executor); err != nil {
		return result, s.rollbackDefinition(ctx, backend, result.ManagedPath, content, true, err)
	}

	result.Installed, result.Current, result.Changed, result.Message = false, false, true, "uninstalled"

	return result, nil
}

func (s *Service) Status(ctx context.Context, opts Options) (Result, error) {
	backend, err := platformBackend(opts)
	if err != nil {
		return Result{}, err
	}

	result := backend.describe()

	content, readErr := os.ReadFile(result.ManagedPath)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			result.Message = "not installed"
			return result, nil
		}

		return result, fmt.Errorf("read managed service: %w", readErr)
	}

	result.Installed = true

	result.Current = string(content) == backend.content()
	if !isManaged(string(content)) {
		return result, fmt.Errorf("%w: %s", ErrForeign, result.ManagedPath)
	}

	state, err := backend.running(ctx, s.executor)
	if err != nil {
		return result, err
	}

	result.Running, result.Message = state.Running, state.Message
	if !result.Current {
		result.Message = "stale; " + result.Message
	} else if result.Running && result.Message == "" {
		result.Message = "running"
	}

	return result, nil
}

// TrackerArgs returns the aht arguments that run the tracker on storePath. A
// zero interval leaves the interval and grace period to the tracker's
// configuration.
func TrackerArgs(storePath string, interval time.Duration, grace time.Duration) []string {
	args := []string{"--store", storePath, "manage", "tracker", "run"}
	if interval > 0 {
		args = append(args, "--interval", interval.String(), "--grace-period", grace.String())
	}

	return append(args, "--quiet")
}

func (s *Service) Update(ctx context.Context, opts Options) (Result, error) {
	backend, err := platformBackend(opts)
	if err != nil {
		return Result{}, err
	}

	result := backend.describe()

	content, installed, err := managedServiceDefinition(result.ManagedPath)
	if err != nil {
		return result, err
	}

	result.Installed = installed

	result.Current = installed && string(content) == backend.content()
	if installed && result.Current {
		return s.applyCurrent(ctx, opts, backend, result)
	}

	if opts.DryRun {
		if installed {
			result.Message = "would update"
		} else {
			result.Message = "would install"
		}

		return result, nil
	}

	if err := s.publishDefinition(
		ctx,
		backend,
		result.ManagedPath,
		content,
		installed,
	); err != nil {
		return result, err
	}

	result.Installed, result.Current, result.Running, result.Changed = true, true, true, true
	if installed {
		result.Message = "updated"
	} else {
		result.Message = "installed"
	}

	return result, nil
}

func (s *Service) publishDefinition(
	ctx context.Context,
	backend backend,
	path string,
	previous []byte,
	installed bool,
) error {
	if err := writeAtomic(path, []byte(backend.content())); err != nil {
		return s.rollbackDefinition(ctx, backend, path, previous, installed, err)
	}

	if installed {
		if err := backend.restart(ctx, s.executor); err != nil {
			return s.rollbackDefinition(ctx, backend, path, previous, true, err)
		}
	} else {
		if err := backend.reload(ctx, s.executor); err != nil {
			return s.rollbackDefinition(ctx, backend, path, previous, false, err)
		}

		if err := backend.load(ctx, s.executor); err != nil {
			return s.rollbackDefinition(ctx, backend, path, previous, false, err)
		}
	}

	return nil
}

func (s *Service) applyCurrent(
	ctx context.Context,
	opts Options,
	backend backend,
	result Result,
) (Result, error) {
	state, err := backend.running(ctx, s.executor)
	if err != nil {
		return result, err
	}

	result.Running = state.Running

	if !state.Loaded {
		if opts.DryRun {
			result.Message = "would start"
			return result, nil
		}

		if err := backend.load(ctx, s.executor); err != nil {
			return result, err
		}

		result.Running, result.Changed, result.Message = true, true, "started"

		return result, nil
	}

	// An unchanged service definition does not imply an unchanged executable,
	// so the tracker restarts to pick up an installed replacement.
	if opts.DryRun {
		result.Message = "would restart"
		return result, nil
	}

	if err := backend.restart(ctx, s.executor); err != nil {
		return result, err
	}

	result.Changed, result.Message = true, "restarted"

	return result, nil
}

func (s *Service) rollbackDefinition(
	ctx context.Context,
	backend backend,
	path string,
	previous []byte,
	previouslyInstalled bool,
	cause error,
) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serviceRollbackTime)
	defer cancel()

	failures := []error{cause}

	if previouslyInstalled {
		if err := writeAtomic(path, previous); err != nil {
			return errors.Join(cause, fmt.Errorf("restoring service definition: %w", err))
		}

		if err := backend.restart(rollbackCtx, s.executor); err != nil {
			failures = append(failures, fmt.Errorf("restarting restored service: %w", err))
		}

		return errors.Join(failures...)
	}

	if err := backend.unload(rollbackCtx, s.executor); err != nil {
		failures = append(failures, fmt.Errorf("unloading failed service installation: %w", err))
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, fmt.Errorf("removing failed service definition: %w", err))
	}

	if err := backend.reload(rollbackCtx, s.executor); err != nil {
		failures = append(failures, fmt.Errorf("reloading manager after service rollback: %w", err))
	}

	return errors.Join(failures...)
}

func managedServiceDefinition(path string) ([]byte, bool, error) {
	content, err := os.ReadFile(path)
	installed := err == nil

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read managed service: %w", err)
	}

	if installed && !isManaged(string(content)) {
		return nil, false, fmt.Errorf("%w: %s", ErrForeign, path)
	}

	return content, installed, nil
}

func writeAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), serviceDirectoryMode); err != nil {
		return fmt.Errorf("create service directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".aht-service-*")
	if err != nil {
		return fmt.Errorf("create temporary service: %w", err)
	}

	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set service mode: %w", err)
	}

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write service: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync service: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close service: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace service: %w", err)
	}

	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open service directory: %w", err)
	}
	defer func() { _ = directory.Close() }()

	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync service directory: %w", err)
	}

	return nil
}

func isManaged(content string) bool {
	if !strings.Contains(content, ManagedMarker) {
		return false
	}

	for line := range strings.Lines(content) {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "# version: ")
		if !ok {
			value, ok = strings.CutPrefix(strings.TrimSpace(line), "<!-- version: ")
			if !ok || !strings.HasSuffix(value, " -->") {
				continue
			}

			value = strings.TrimSuffix(value, " -->")
		}

		version, err := strconv.Atoi(value)
		if err == nil && version >= 2 && version <= ManagedVersion {
			return true
		}
	}

	return false
}

func managerMissing(output []byte) bool {
	message := strings.ToLower(string(output))

	return strings.Contains(message, "not loaded") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "could not find")
}

func wrapManagerError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}

	return fmt.Errorf("%s: %w (%s)", action, err, message)
}

func trackerCommand(opts Options) []string {
	return append(
		[]string{opts.Binary},
		TrackerArgs(opts.StorePath, opts.Interval, opts.GracePeriod)...,
	)
}

func describeDefinition(path string) Result {
	return Result{
		Platform:       platformName,
		Manager:        managerName,
		ManagedPath:    path,
		ManagedVersion: ManagedVersion,
		Installed:      false,
		Current:        false,
		Running:        false,
		Changed:        false,
		Message:        "",
	}
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.Binary == "" {
		return opts, errBinaryRequired
	}

	if opts.StorePath == "" {
		return opts, errStorePathRequired
	}

	binary := opts.Binary
	if filepath.Base(binary) == binary {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			return opts, fmt.Errorf("find binary %q: %w", binary, err)
		}

		binary = resolved
	}

	binary, err := filepath.Abs(binary)
	if err != nil {
		return opts, fmt.Errorf("resolve binary: %w", err)
	}

	store, err := filepath.Abs(opts.StorePath)
	if err != nil {
		return opts, fmt.Errorf("resolve store: %w", err)
	}

	if opts.Interval <= 0 {
		return opts, errIntervalPositive
	}

	if opts.GracePeriod < 0 {
		return opts, errGraceNonnegative
	}

	opts.Binary, opts.StorePath = binary, store

	return opts, nil
}
