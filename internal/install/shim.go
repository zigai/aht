package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	harnesscatalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

const shimFileMode = 0o700

var errRecursiveShimTarget = errors.New("target binary resolves to managed shim")

func installShim(options Options, harness registry.Harness) (Result, error) {
	dir := filepath.Join(registry.DefaultStateDir(), "shims")
	path := filepath.Join(dir, string(harness))
	target, err := resolveShimTarget(options.TargetBinary, string(harness), dir, path)
	if err != nil {
		return Result{}, err
	}
	script := shimScript(options.Binary, string(harness), target, harnesscatalog.IntegrationVersionFor(harness))

	contentChanged, err := fileNeedsUpdate(path, script, options.Force)
	if err != nil {
		return Result{}, err
	}
	modeChanged, err := shimModeNeedsUpdate(path)
	if err != nil {
		return Result{}, err
	}
	changed := contentChanged || modeChanged

	if changed && !options.DryRun {
		if err := writeShimChanges(path, script, contentChanged); err != nil {
			return Result{}, err
		}
	}

	message := fmt.Sprintf("%s shim installed; put %s before the real harness binary in PATH", harness, dir)
	if !changed {
		message = "already installed"
	}

	if options.DryRun {
		message = fmt.Sprintf("dry run: %s shim not written", harness)
	}

	return Result{
		Harness:  string(harness),
		Path:     path,
		Changed:  changed,
		Message:  message,
		NextStep: "",
		Snippet:  script,
		Error:    "",
	}, nil
}

func writeShimChanges(path string, script string, contentChanged bool) error {
	if contentChanged {
		return writeFileAtomicMode(path, []byte(script), shimFileMode, "creating shim directory", "writing shim")
	}
	if err := os.Chmod(path, shimFileMode); err != nil {
		return fmt.Errorf("making shim executable: %w", err)
	}

	return nil
}

func shimModeNeedsUpdate(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}

		return false, fmt.Errorf("checking shim mode: %w", err)
	}

	return info.Mode().Perm() != shimFileMode, nil
}

func classifyShimArtifact(path string) (ArtifactStatus, error) {
	status, err := ClassifyArtifact(path)
	if err != nil || status != ArtifactCurrent {
		return status, err
	}
	modeChanged, err := shimModeNeedsUpdate(path)
	if err != nil {
		return "", err
	}
	if modeChanged {
		return ArtifactStale, nil
	}

	return status, nil
}

func resolveShimTarget(target string, harness string, shimDir string, shimPath string) (string, error) {
	if target != "" {
		if pathInDir(target, shimDir) || samePath(target, shimPath) {
			return "", fmt.Errorf("%w: %s", errRecursiveShimTarget, target)
		}

		return target, nil
	}

	found, err := lookPathExcludingShimDir(harness, shimDir)
	if err != nil {
		return "", fmt.Errorf("finding %s binary: %w", harness, err)
	}
	if pathInDir(found, shimDir) || samePath(found, shimPath) {
		return "", fmt.Errorf("%w: %s", errRecursiveShimTarget, found)
	}

	return found, nil
}

func lookPathExcludingShimDir(file string, shimDir string) (string, error) {
	if strings.ContainsAny(file, `/\`) {
		if isExecutable(file) {
			return file, nil
		}

		return "", os.ErrNotExist
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" || samePath(dir, shimDir) {
			continue
		}
		candidate := filepath.Join(dir, file)
		if isExecutable(candidate) {
			return candidate, nil
		}
	}

	return "", os.ErrNotExist
}

func isExecutable(path string) bool {
	info, err := os.Stat(filepath.Clean(path))
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func pathInDir(path string, dir string) bool {
	path = canonicalPath(path)
	dir = canonicalPath(dir)
	relative, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}

	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func samePath(left string, right string) bool {
	left = canonicalPath(left)
	right = canonicalPath(right)
	return left == right
}

func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	evaluated, err := filepath.EvalSymlinks(path)
	if err == nil {
		path = evaluated
	}

	return filepath.Clean(path)
}

func shimScript(binary string, harness string, target string, version int) string {
	return fmt.Sprintf(`#!/bin/sh
set -u

aht_managed_marker=%s
AHT_INTEGRATION_ID=%s-shim
AHT_INTEGRATION_VERSION=%d
aht_bin=%s
harness_bin=%s

"$aht_bin" report %s --presence live --evidence process --pid "$$" --event process.start --reporter-version %d --reporter %s-shim --quiet >/dev/null 2>&1 || true
"$harness_bin" "$@"
status=$?
"$aht_bin" report %s --presence gone --evidence process --pid "$$" --event process.exit --reporter-version %d --reporter %s-shim --quiet >/dev/null 2>&1 || true
exit "$status"
`, harnesspkg.ShellQuote(managedMarker), harness, version, harnesspkg.ShellQuote(binary), harnesspkg.ShellQuote(target), harnesspkg.ShellQuote(harness), version, harness, harnesspkg.ShellQuote(harness), version, harness)
}
