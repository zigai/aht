package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

const shimFileMode = 0o700

var errRecursiveShimTarget = errors.New("target binary resolves to managed shim")

func installShim(opts Options, harnessID registry.Harness) (Result, error) {
	dir := filepath.Join(registry.DefaultStateDir(), "shims")
	path := filepath.Join(dir, string(harnessID))

	executable := string(harnessID)
	if names := adapters.ProcessNames(harnessID); len(names) > 0 && names[0] != "" {
		executable = names[0]
	}

	target, err := resolveShimTarget(opts.TargetBinary, executable, dir, path)
	if err != nil {
		return Result{}, err
	}

	script := shimScript(
		opts.Binary,
		harnessID,
		target,
		adapters.IntegrationVersionFor(harnessID),
	)

	contentChanged, err := shimContentNeedsUpdate(path, script, harnessID, opts.Force)
	if err != nil {
		return Result{}, err
	}

	modeChanged, err := shimModeNeedsUpdate(path)
	if err != nil {
		return Result{}, err
	}

	changed := contentChanged || modeChanged

	if changed && !opts.DryRun {
		if err := writeShimChanges(path, script, contentChanged); err != nil {
			return Result{}, err
		}
	}

	message := fmt.Sprintf(
		"%s shim installed; put %s before the real harness binary in PATH",
		harnessID,
		dir,
	)
	if !changed {
		message = "already installed"
	}

	if opts.DryRun {
		message = fmt.Sprintf("dry run: %s shim not written", harnessID)
	}

	return Result{
		Harness:  string(harnessID),
		Path:     path,
		Changed:  changed,
		Message:  message,
		NextStep: "",
		Snippet:  script,
		Error:    "",
	}, nil
}

func shimContentNeedsUpdate(
	path string,
	script string,
	id registry.Harness,
	force bool,
) (bool, error) {
	status, err := classifyShimArtifact(path, id, "")
	if err != nil {
		return false, err
	}

	if status == ArtifactForeign && !force {
		return false, fmt.Errorf("%w: %s; pass --force to replace it", errForeignFile, path)
	}

	return fileNeedsUpdate(path, script, true)
}

func writeShimChanges(path string, script string, contentChanged bool) error {
	if contentChanged {
		return harness.WriteFileAtomicMode(
			path,
			[]byte(script),
			shimFileMode,
			"creating shim directory",
			"writing shim",
		)
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

func classifyShimArtifact(
	path string,
	harnessID registry.Harness,
	binary string,
) (ArtifactStatus, error) {
	status, err := classifyArtifactWith(path, func(content string) ArtifactStatus {
		status := classifyExpectedIdentity(
			content,
			shimIdentity(harnessID, adapters.IntegrationVersionFor(harnessID)),
		)
		if status == ArtifactCurrent && binary != "" &&
			!strings.Contains(content, "\naht_bin="+harness.ShellQuote(binary)+"\n") {
			return ArtifactStale
		}

		return status
	})
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

func resolveShimTarget(
	target string,
	harnessID string,
	shimDir string,
	shimPath string,
) (string, error) {
	if target != "" {
		absolute, err := absolutePath(target)
		if err != nil {
			return "", err
		}

		if pathInDir(absolute, shimDir) || samePath(absolute, shimPath) {
			return "", fmt.Errorf("%w: %s", errRecursiveShimTarget, absolute)
		}

		return absolute, nil
	}

	found, err := lookPathExcludingShimDir(harnessID, shimDir)
	if err != nil {
		return "", fmt.Errorf("finding %s binary: %w", harnessID, err)
	}

	if pathInDir(found, shimDir) || samePath(found, shimPath) {
		return "", fmt.Errorf("%w: %s", errRecursiveShimTarget, found)
	}

	return found, nil
}

func lookPathExcludingShimDir(file string, shimDir string) (string, error) {
	if strings.ContainsAny(file, `/\`) {
		if isExecutable(file) {
			return absolutePath(file)
		}

		return "", os.ErrNotExist
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" || samePath(dir, shimDir) {
			continue
		}

		candidate := filepath.Join(dir, file)
		if isExecutable(candidate) {
			return absolutePath(candidate)
		}
	}

	return "", os.ErrNotExist
}

func absolutePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	return absolute, nil
}

func isExecutable(path string) bool {
	//nolint:gosec // G703: entries of the user's own PATH.
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

	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
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

func shimScript(binary string, harnessID registry.Harness, target string, version int) string {
	identity := shimIdentity(harnessID, version)

	//nolint:lll // multi-line generated shell must preserve exact bytes.
	return fmt.Sprintf(`#!/bin/sh
set -u

%saht_bin=%s
harness_bin=%s

"$aht_bin" report %s --presence live --evidence process --pid "$$" --event process.start --reporter-version %d --reporter %s --quiet >/dev/null 2>&1 || true
"$harness_bin" "$@"
status=$?
"$aht_bin" report %s --presence gone --evidence process --pid "$$" --event process.exit --reporter-version %d --reporter %s --quiet >/dev/null 2>&1 || true
exit "$status"
`, identity.Marker("# "), harness.ShellQuote(
		binary,
	), harness.ShellQuote(
		target,
	), harness.ShellQuote(
		string(harnessID),
	), version, identity.Source, harness.ShellQuote(
		string(harnessID),
	), version, identity.Source)
}

func shimIdentity(id registry.Harness, version int) harness.ArtifactIdentity {
	return harness.ArtifactIdentity{
		ID:      string(id) + "-shim",
		Version: version,
		Source:  string(id) + "-shim",
	}
}
