package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultBinary = "aht"
	managedMarker = harnesspkg.ManagedMarker
)

var (
	errUnsupportedHarness = errors.New("unsupported harness")
	errForeignFile        = errors.New("file exists and is not managed by aht")
	errBinaryEncoding     = errors.New("aht binary path is not valid UTF-8")
	errManagedBlockMarker = errors.New("managed block markers are not paired")
)

//exhaustruct:optional
type Options struct {
	preserveRegistration bool
	Harness              registry.Harness
	Binary               string
	TargetBinary         string
	DryRun               bool
	Force                bool
	UseShim              bool
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

// Run installs one integration while honoring caller cancellation.
func Run(ctx context.Context, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("install integration context: %w", err)
	}

	if opts.Binary == "" {
		opts.Binary = defaultBinary
	}

	return installHarnessAdapter(ctx, opts)
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
		currentIdentity, _ := harnesspkg.ParseArtifactIdentity(string(current))

		nextIdentity, _ := harnesspkg.ParseArtifactIdentity(content)
		if currentIdentity.ID != "" && nextIdentity.ID != "" &&
			currentIdentity.ID != nextIdentity.ID {
			return false, fmt.Errorf(
				"%w: %s is managed by the %s integration; pass --force to replace it",
				errForeignFile,
				path,
				currentIdentity.ID,
			)
		}
	}

	return true, nil
}
