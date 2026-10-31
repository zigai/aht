// Package launch runs an installed harness against a fake model provider.
package launch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultModel     = "fake"
	defaultAPIKey    = "fake"
	commandWaitDelay = time.Second
	logTailBytes     = 4 << 10
)

// ErrUnsupported reports a harness that cannot be launched as requested.
var ErrUnsupported = errors.New("harness does not support fake provider launches")

var (
	errRelativeRoot     = errors.New("launch root must be an absolute path")
	errRootNotEmpty     = errors.New("launch root is not empty")
	errEndpoint         = errors.New("endpoint must be an http or https origin without a path")
	errEndpointRequired = errors.New("endpoint is required for a harness with a launch plan")
	errNoExecutable     = errors.New("launch plan names no executable")
	errNoAHT            = errors.New("launch has no AHT executable")
	errTrackerRun       = errors.New("tracker is already running")
	errNilMatch         = errors.New("session matcher is nil")
	errNoFileUpdate     = errors.New("launch file has no update function")
)

var rootDirectories = []string{"home", "work", "tmp", "config", "data", "state", "cache", "logs"}

// Options configures a Harness.
type Options struct {
	// Harness selects the adapter whose launch plan runs.
	Harness registry.Harness
	// Root must be absolute and missing or empty.
	Root string
	// Endpoint is the provider origin, without /v1.
	Endpoint string
	// Model is the model id the harness requests. Empty means "fake".
	Model string
	// APIKey is the provider credential given to the harness. Empty means "fake".
	APIKey string
	// AHT is the aht executable; empty skips the integration and tracker.
	AHT string
	// Executable overrides the plan's executable.
	Executable string
	// Env holds extra KEY=VALUE entries applied last.
	Env []string
}

// Tool is the native shell tool a scripted model can call.
type Tool struct {
	Name string
	// Arguments builds the native tool input that runs command.
	Arguments func(command string) map[string]any
}

// Harness is one harness launch prepared under Root.
type Harness struct {
	ID         registry.Harness
	Root       string
	Home       string
	Work       string
	Store      string
	Executable string
	Protocol   fakeprovider.Protocol
	// Env is the complete environment of every process the launch starts.
	Env       []string
	ShellTool Tool

	plan     harness.LaunchPlan
	aht      string
	services []*process
	tracker  *process
}

// New prepares Root and starts the plan's services.
func New(ctx context.Context, options Options) (*Harness, error) {
	adapter, found := catalog.Find(options.Harness)
	if !found {
		return nil, fmt.Errorf("%w: harness %q", ErrUnsupported, options.Harness)
	}

	planner, _ := adapter.(harness.Launchable)

	return newHarness(ctx, options, planner)
}

func newHarness(
	ctx context.Context,
	options Options,
	planner harness.Launchable,
) (*Harness, error) {
	if err := options.validate(planner != nil); err != nil {
		return nil, err
	}

	root := filepath.Clean(options.Root)
	if err := checkRoot(root); err != nil {
		return nil, err
	}

	aht := ""

	if options.AHT != "" {
		resolved, err := resolveExecutable(options.AHT)
		if err != nil {
			return nil, err
		}

		aht = resolved
	}

	paths := harness.LaunchPaths{
		Root: root,
		Home: filepath.Join(root, "home"),
		Work: filepath.Join(root, "work"),
	}

	var plan harness.LaunchPlan

	if planner != nil {
		model := harness.LaunchModel{
			Endpoint: options.Endpoint,
			Model:    valueOr(options.Model, defaultModel),
			APIKey:   valueOr(options.APIKey, defaultAPIKey),
		}

		var err error

		plan, err = planner.LaunchPlan(paths, model)
		if err != nil {
			return nil, fmt.Errorf("launch plan for %q: %w", options.Harness, err)
		}
	}

	store := filepath.Join(root, "sessions.json")

	env, err := mergeEnvironment(commonEnvironment(root, store), plan.Env, options.Env)
	if err != nil {
		return nil, err
	}

	executable, err := chooseExecutable(options, plan, planner != nil)
	if err != nil {
		return nil, err
	}

	if err := createRootLayout(root); err != nil {
		return nil, err
	}

	h := &Harness{
		ID:         options.Harness,
		Root:       root,
		Home:       paths.Home,
		Work:       paths.Work,
		Store:      store,
		Executable: executable,
		Protocol:   plan.Protocol,
		Env:        env,
		ShellTool:  Tool{Name: plan.ShellTool.Name, Arguments: plan.ShellTool.Arguments},
		plan:       plan,
		aht:        aht,
		services:   nil,
		tracker:    nil,
	}
	if err := h.prepare(ctx); err != nil {
		return nil, errors.Join(err, h.Close())
	}

	return h, nil
}

func chooseExecutable(options Options, plan harness.LaunchPlan, hasPlan bool) (string, error) {
	name := valueOr(options.Executable, plan.Executable)
	if name == "" {
		if !hasPlan {
			return "", fmt.Errorf(
				"%w: harness %q has no launch plan and no executable",
				ErrUnsupported,
				options.Harness,
			)
		}

		return "", fmt.Errorf("launch plan for %q: %w", options.Harness, errNoExecutable)
	}

	return resolveExecutable(name)
}

// Command returns a harness command in Work with the launch environment.
func (h *Harness) Command(ctx context.Context, args ...string) *exec.Cmd {
	return h.configure(exec.CommandContext(ctx, h.Executable, args...))
}

// AHTCommand returns an aht command for the launch store.
func (h *Harness) AHTCommand(ctx context.Context, args ...string) *exec.Cmd {
	return h.configure(
		exec.CommandContext(ctx, h.aht, append([]string{"--store", h.Store}, args...)...),
	)
}

// HeadlessArgs returns the non-interactive launch arguments for prompt.
func (h *Harness) HeadlessArgs(prompt string) ([]string, error) {
	if h.plan.Headless == nil {
		return nil, fmt.Errorf("%w: %q has no headless launch", ErrUnsupported, h.ID)
	}

	args, err := h.plan.Headless(prompt)
	if err != nil {
		return nil, fmt.Errorf("headless arguments for %q: %w", h.ID, err)
	}

	return args, nil
}

// InteractiveArgs returns the terminal UI arguments for prompt.
func (h *Harness) InteractiveArgs(prompt string) ([]string, error) {
	if h.plan.Interactive == nil {
		return nil, fmt.Errorf("%w: %q has no interactive launch", ErrUnsupported, h.ID)
	}

	args, err := h.plan.Interactive(prompt)
	if err != nil {
		return nil, fmt.Errorf("interactive arguments for %q: %w", h.ID, err)
	}

	return args, nil
}

// Close stops every process of the launch and leaves Root on disk.
func (h *Harness) Close() error {
	errs := []error{h.stopTracker()}
	for _, process := range slices.Backward(h.services) {
		errs = append(errs, process.stop())
	}

	h.services = nil
	errs = append(errs, h.stopStrays())

	return errors.Join(errs...)
}

func (h *Harness) prepare(ctx context.Context) error {
	if h.aht != "" {
		cmd := h.AHTCommand(
			ctx,
			"manage",
			"integrations",
			"install",
			string(h.ID),
			"--binary",
			h.aht,
		)
		if err := h.runLogged(cmd, "aht-install"); err != nil {
			return err
		}
	}

	if err := applyFiles(h.Root, h.plan.Files); err != nil {
		return err
	}

	for index, args := range h.plan.Setup {
		if err := h.runLogged(h.Command(ctx, args...), "setup-"+strconv.Itoa(index)); err != nil {
			return err
		}
	}

	for index, service := range h.plan.Services {
		if err := h.startService(ctx, index, service); err != nil {
			return err
		}
	}

	return nil
}

func (h *Harness) configure(cmd *exec.Cmd) *exec.Cmd {
	cmd.Env = h.Env
	cmd.Dir = h.Work
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = commandWaitDelay

	return cmd
}

func (options Options) validate(hasPlan bool) error {
	if !filepath.IsAbs(options.Root) {
		return fmt.Errorf("root %q: %w", options.Root, errRelativeRoot)
	}

	if options.Endpoint == "" {
		if hasPlan {
			return errEndpointRequired
		}

		return nil
	}

	return validateEndpoint(options.Endpoint)
}

func validateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("endpoint %q: %w", endpoint, errEndpoint)
	}

	return nil
}

func checkRoot(root string) error {
	entries, err := os.ReadDir(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("read launch root: %w", err)
	case len(entries) > 0:
		return fmt.Errorf("root %q: %w", root, errRootNotEmpty)
	}

	return nil
}

func createRootLayout(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create launch root: %w", err)
	}

	for _, name := range rootDirectories {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			return fmt.Errorf("create launch directory %s: %w", name, err)
		}
	}

	return nil
}

func resolveExecutable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", name, err)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", name, err)
	}

	return absolute, nil
}

func valueOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
