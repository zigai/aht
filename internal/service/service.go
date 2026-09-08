package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/internal/command"
)

const (
	ManagedVersion            = 7
	ManagedMarker             = "aht managed observer service"
	defaultInterval           = 300 * time.Millisecond
	serviceDirectoryMode      = 0o755
	serviceRollbackTime       = 10 * time.Second
	serviceCommandTimeout     = 30 * time.Second
	serviceCommandOutputLimit = 4 << 20
)

var (
	ErrForeign           = errors.New("service path contains foreign content")
	ErrUnsupported       = errors.New("observer service is unsupported on this platform")
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
	Path           string `json:"-"`
	Version        int    `json:"-"`
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
	output, err := command.RunWithLimits(ctx, serviceCommandTimeout, serviceCommandOutputLimit, name, nil, args...)
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

func Install(ctx context.Context, options Options) (Result, error) {
	return defaultService.Install(ctx, options)
}

func Update(ctx context.Context, options Options) (Result, error) {
	return defaultService.Update(ctx, options)
}

func Uninstall(ctx context.Context, options Options) (Result, error) {
	return defaultService.Uninstall(ctx, options)
}

func Status(ctx context.Context, options Options) (Result, error) {
	return defaultService.Status(ctx, options)
}

func (s *Service) Install(ctx context.Context, options Options) (Result, error) {
	return s.apply(ctx, options, false)
}

func (s *Service) Update(ctx context.Context, options Options) (Result, error) {
	return s.apply(ctx, options, true)
}

func (s *Service) Uninstall(ctx context.Context, options Options) (Result, error) {
	backend, err := platformBackend(options)
	if err != nil {
		return Result{}, err
	}
	result := backend.describe()
	content, readErr := os.ReadFile(result.Path)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			result.Message = "not installed"
			return result, nil
		}
		return result, fmt.Errorf("read managed service: %w", readErr)
	}
	if !isManaged(string(content)) {
		return result, fmt.Errorf("%w: %s", ErrForeign, result.Path)
	}
	result.Installed, result.Current = true, string(content) == backend.content()
	if options.DryRun {
		result.Message = "would uninstall"
		return result, nil
	}
	if err := backend.unload(ctx, s.executor); err != nil {
		return result, err
	}
	if err := os.Remove(result.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		cause := fmt.Errorf("remove managed service: %w", err)

		return result, s.rollbackDefinition(ctx, backend, result.Path, content, true, cause)
	}
	if err := backend.reload(ctx, s.executor); err != nil {
		return result, s.rollbackDefinition(ctx, backend, result.Path, content, true, err)
	}
	result.Installed, result.Current, result.Changed, result.Message = false, false, true, "uninstalled"
	return result, nil
}

func (s *Service) Status(ctx context.Context, options Options) (Result, error) {
	backend, err := platformBackend(options)
	if err != nil {
		return Result{}, err
	}
	result := backend.describe()
	content, readErr := os.ReadFile(result.Path)
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
		return result, fmt.Errorf("%w: %s", ErrForeign, result.Path)
	}
	running, message, err := backend.running(ctx, s.executor)
	if err != nil {
		return result, err
	}
	result.Running, result.Message = running, message
	if !result.Current {
		result.Message = "stale; " + result.Message
	} else if result.Running && result.Message == "" {
		result.Message = "running"
	}
	return result, nil
}

//nolint:gocognit,cyclop // service installation coordinates platform manager and atomic file transitions
func (s *Service) apply(ctx context.Context, options Options, update bool) (Result, error) {
	backend, err := platformBackend(options)
	if err != nil {
		return Result{}, err
	}
	result := backend.describe()
	content, readErr := os.ReadFile(result.Path)
	installed := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return result, fmt.Errorf("read managed service: %w", readErr)
	}
	if installed && !isManaged(string(content)) {
		return result, fmt.Errorf("%w: %s", ErrForeign, result.Path)
	}
	result.Installed = installed
	result.Current = installed && string(content) == backend.content()
	if installed && result.Current {
		return s.applyCurrent(ctx, options, backend, result, update)
	}
	if installed && !result.Current && !update {
		result.Message = "stale; run update"
		return result, nil
	}
	if options.DryRun {
		if installed {
			result.Message = "would update"
		} else {
			result.Message = "would install"
		}
		return result, nil
	}
	if err := writeAtomic(result.Path, []byte(backend.content())); err != nil {
		return result, s.rollbackDefinition(ctx, backend, result.Path, content, installed, err)
	}
	if err := backend.reload(ctx, s.executor); err != nil {
		return result, s.rollbackDefinition(ctx, backend, result.Path, content, installed, err)
	}
	if installed {
		if err := backend.restart(ctx, s.executor); err != nil {
			return result, s.rollbackDefinition(ctx, backend, result.Path, content, true, err)
		}
	} else if err := backend.load(ctx, s.executor); err != nil {
		return result, s.rollbackDefinition(ctx, backend, result.Path, content, false, err)
	}
	result.Installed, result.Current, result.Running, result.Changed = true, true, true, true
	if installed {
		result.Message = "updated"
	} else {
		result.Message = "installed"
	}
	return result, nil
}

func (s *Service) applyCurrent(ctx context.Context, options Options, backend backend, result Result, update bool) (Result, error) {
	running, _, err := backend.running(ctx, s.executor)
	if err != nil {
		return result, err
	}
	if !running {
		if options.DryRun {
			result.Message = "would start"
			return result, nil
		}
		if err := backend.load(ctx, s.executor); err != nil {
			return result, err
		}
		result.Running, result.Changed, result.Message = true, true, "started"
		return result, nil
	}

	result.Running = true
	// An unchanged service definition does not imply an unchanged executable.
	// Explicit updates restart the tracker so an installed replacement takes effect.
	if update {
		if options.DryRun {
			result.Message = "would restart"
			return result, nil
		}
		if err := backend.restart(ctx, s.executor); err != nil {
			return result, err
		}
		result.Changed, result.Message = true, "restarted"
		return result, nil
	}
	result.Message = "already enabled"
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
		if err := backend.reload(rollbackCtx, s.executor); err != nil {
			failures = append(failures, fmt.Errorf("reloading restored service definition: %w", err))
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
		switch strings.TrimSpace(line) {
		case "# version: 2", "# version: 3", "# version: 4", "# version: 5", "# version: 6", "# version: 7",
			"<!-- version: 2 -->", "<!-- version: 3 -->", "<!-- version: 4 -->", "<!-- version: 5 -->", "<!-- version: 6 -->", "<!-- version: 7 -->":
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

func normalizeOptions(options Options) (Options, error) {
	if options.Binary == "" {
		return options, errBinaryRequired
	}
	if options.StorePath == "" {
		return options, errStorePathRequired
	}
	binary := options.Binary
	if filepath.Base(binary) == binary {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			return options, fmt.Errorf("find binary %q: %w", binary, err)
		}
		binary = resolved
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		return options, fmt.Errorf("resolve binary: %w", err)
	}
	store, err := filepath.Abs(options.StorePath)
	if err != nil {
		return options, fmt.Errorf("resolve store: %w", err)
	}
	if options.Interval == 0 {
		options.Interval = defaultInterval
	}
	if options.Interval <= 0 {
		return options, errIntervalPositive
	}
	if options.GracePeriod < 0 {
		return options, errGraceNonnegative
	}
	options.Binary, options.StorePath = binary, store
	return options, nil
}
