package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	harnesscatalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultBinary = "aht"
	managedMarker = harnesspkg.ManagedMarker
)

var (
	errUnsupportedHarness = errors.New("unsupported harness")
	errForeignFile        = errors.New("file exists and is not managed by aht")
)

var allHarnesses = installableHarnesses()

type Options struct {
	Harness      registry.Harness
	Binary       string
	TargetBinary string
	DryRun       bool
	Force        bool
	UseShim      bool
}

type Result struct {
	Harness  string `json:"harness"`
	Path     string `json:"path"`
	Changed  bool   `json:"changed"`
	Message  string `json:"message"`
	NextStep string `json:"next_step,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	Error    string `json:"error,omitempty"`
}

// AllHarnesses returns a snapshot of the installable harness catalog.
func AllHarnesses() []registry.Harness {
	return slices.Clone(allHarnesses)
}

func Run(opts Options) (Result, error) {
	return RunContext(context.Background(), opts)
}

// RunContext installs one integration while honoring caller cancellation.
func RunContext(ctx context.Context, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("install integration context: %w", err)
	}
	if opts.Binary == "" {
		opts.Binary = defaultBinary
	}

	return installHarnessAdapter(ctx, opts)
}

func installableHarnesses() []registry.Harness {
	harnesses := make([]registry.Harness, 0, len(harnesscatalog.All()))
	for _, adapter := range harnesscatalog.All() {
		if _, ok := adapter.(harnesspkg.Installable); ok {
			harnesses = append(harnesses, adapter.Definition().ID)
		}
	}

	return harnesses
}

func fileNeedsUpdate(path string, content string, force bool) (bool, error) {
	current, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}

		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	if string(current) == content {
		return false, nil
	}

	if !force && !strings.Contains(string(current), managedMarker) {
		return false, fmt.Errorf("%w: %s; pass --force to replace it", errForeignFile, path)
	}
	if !force {
		currentIntegration := integrationIDFromContent(string(current))
		nextIntegration := integrationIDFromContent(content)
		if currentIntegration != "" && nextIntegration != "" && currentIntegration != nextIntegration {
			return false, fmt.Errorf(
				"%w: %s is managed by the %s integration; pass --force to replace it",
				errForeignFile,
				path,
				currentIntegration,
			)
		}
	}

	return true, nil
}
