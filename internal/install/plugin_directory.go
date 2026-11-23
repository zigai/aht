package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

const (
	pluginBackupMarker   = ".aht-backup"
	pluginBackupOriginal = "original"
	maxPluginMarkerBytes = 4096
)

var (
	errDuplicatePluginFileName = errors.New("duplicate plugin file name")
	errInvalidPluginFileName   = errors.New("invalid plugin file name")
	errStalePluginFile         = errors.New("stale plugin file")
)

//exhaustruct:optional
type pluginDirectoryInstall struct {
	dir          string
	identity     harness.ArtifactIdentity
	files        map[string]string
	snippetOrder []string
	renameFile   func(string, string) error
	removeTree   func(string) error
}

type pluginRecovery struct {
	cleanup []string
	restore string
	newest  os.FileInfo
}

func renderInstallFiles(
	specs []harness.RenderedFileInstallSpec,
	kind string,
) (map[string]string, error) {
	files := make(map[string]string, len(specs))
	for _, spec := range specs {
		if err := validateInstallRelativePath(spec.Name); err != nil {
			return nil, err
		}

		content, err := renderInstallContent(spec.Content, spec.JSONContent)
		if err != nil {
			return nil, fmt.Errorf("encoding %s file %s: %w", kind, spec.Name, err)
		}

		if _, exists := files[spec.Name]; exists {
			return nil, fmt.Errorf("%w: %q", errDuplicatePluginFileName, spec.Name)
		}

		files[spec.Name] = content
	}

	return files, nil
}

func validateInstallRelativePath(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || filepath.IsAbs(trimmed) || strings.Contains(trimmed, `\`) {
		return fmt.Errorf("%w: %q", errInvalidPluginFileName, name)
	}

	cleaned := filepath.Clean(trimmed)
	if cleaned != trimmed || cleaned == "." {
		return fmt.Errorf("%w: %q", errInvalidPluginFileName, name)
	}

	for part := range strings.SplitSeq(cleaned, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: %q", errInvalidPluginFileName, name)
		}
	}

	return nil
}

// renderPluginFiles renders the plan's files followed by its marker file, in
// install order.
func renderPluginFiles(
	plan harness.PluginDirectoryInstallPlan,
) (map[string]string, []string, error) {
	specs := append(slices.Clone(plan.Files), harness.RenderedFileInstallSpec{
		Name:    harness.ArtifactMarkerFile,
		Content: plan.Identity.Marker(""),
	})

	files, err := renderInstallFiles(specs, "plugin")
	if err != nil {
		return nil, nil, err
	}

	order := make([]string, 0, len(specs))
	for _, spec := range specs {
		order = append(order, spec.Name)
	}

	return files, order, nil
}

func newPluginDirectoryInstall(
	plan harness.PluginDirectoryInstallPlan,
	files map[string]string,
	order []string,
) pluginDirectoryInstall {
	return pluginDirectoryInstall{
		dir:          plan.Dir,
		identity:     plan.Identity,
		files:        files,
		snippetOrder: order,
		renameFile:   os.Rename,
		removeTree:   os.RemoveAll,
	}
}

func (plugin pluginDirectoryInstall) rename(oldPath string, newPath string) error {
	var err error
	if plugin.renameFile != nil {
		err = plugin.renameFile(oldPath, newPath)
	} else {
		err = os.Rename(oldPath, newPath)
	}

	if err != nil {
		return fmt.Errorf("renaming %s to %s: %w", oldPath, newPath, err)
	}

	return nil
}

func (plugin pluginDirectoryInstall) removeAll(path string) error {
	var err error
	if plugin.removeTree != nil {
		err = plugin.removeTree(path)
	} else {
		err = os.RemoveAll(path)
	}

	if err != nil {
		return fmt.Errorf("removing directory tree %s: %w", path, err)
	}

	return nil
}

func (plugin pluginDirectoryInstall) applyRecovery(recovery pluginRecovery) error {
	parent := filepath.Dir(plugin.dir)
	if recovery.restore != "" {
		if err := plugin.rename(recovery.restore, plugin.dir); err != nil {
			return err
		}

		if err := harness.SyncDir(filepath.Dir(recovery.restore)); err != nil {
			return err
		}

		if err := harness.SyncDir(parent); err != nil {
			return err
		}
	}

	for _, path := range recovery.cleanup {
		if err := plugin.removeAll(path); err != nil {
			return err
		}
	}

	return harness.SyncDir(parent)
}

func (plugin pluginDirectoryInstall) inspectRecovery() (pluginRecovery, error) {
	var recovery pluginRecovery

	parent := filepath.Dir(plugin.dir)

	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return recovery, nil
	}

	if err != nil {
		return recovery, fmt.Errorf("reading plugin recovery directory: %w", err)
	}

	_, err = os.Lstat(plugin.dir)

	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return recovery, fmt.Errorf("checking plugin recovery target: %w", err)
	}

	for _, entry := range entries {
		if err := plugin.collectRecovery(&recovery, entry, missing); err != nil {
			return recovery, err
		}
	}

	return recovery, nil
}

func (plugin pluginDirectoryInstall) collectRecovery(
	recovery *pluginRecovery,
	entry os.DirEntry,
	missing bool,
) error {
	if !entry.IsDir() {
		return nil
	}

	backup := plugin.temporaryName(entry.Name(), "backup")
	if !backup && !plugin.temporaryName(entry.Name(), "stage") {
		return nil
	}

	path := filepath.Join(filepath.Dir(plugin.dir), entry.Name())

	original, owned, err := plugin.recoveryCandidate(path, backup)
	if err != nil || !owned {
		return err
	}

	recovery.cleanup = append(recovery.cleanup, path)

	if !missing || original == "" {
		return nil
	}

	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("checking plugin backup: %w", err)
	}

	if recovery.newest == nil || info.ModTime().After(recovery.newest.ModTime()) {
		recovery.restore, recovery.newest = original, info
	}

	return nil
}

func (plugin pluginDirectoryInstall) recoveryCandidate(
	path string,
	backup bool,
) (string, bool, error) {
	if backup {
		marker, err := readPluginRecoveryMarker(filepath.Join(path, pluginBackupMarker))
		if err != nil {
			return "", false, err
		}

		if marker == plugin.backupProvenance() {
			return inspectBackupContainer(path)
		}
	}

	// Legacy backups were the original directory itself. A temp-looking
	// name alone cannot distinguish a forced markerless backup from user data.
	marker, err := readPluginRecoveryMarker(filepath.Join(path, harness.ArtifactMarkerFile))
	if err != nil {
		return "", false, err
	}

	owned := plugin.ownsRecoveryMarker(marker)
	if owned && backup {
		return path, true, nil
	}

	return "", owned, nil
}

func (plugin pluginDirectoryInstall) ownsRecoveryMarker(marker string) bool {
	identity, _ := harness.ParseArtifactIdentity(marker)
	if identity.Version <= 0 || identity.ID == "" || identity.ID != plugin.identity.ID {
		return false
	}

	if identity.Source == "" {
		// Older plugin markers identify the harness and version but omit
		// the source. Match that exact generated marker shape as well.
		legacy := strings.TrimSuffix(identity.Marker(""), "AHT_SOURCE=\n")

		return strings.TrimSuffix(marker, "\n") == strings.TrimSuffix(legacy, "\n")
	}

	return identity.Source == plugin.identity.Source && marker == identity.Marker("")
}

func readPluginRecoveryMarker(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("checking plugin recovery marker: %w", err)
	}

	if !info.Mode().IsRegular() || info.Size() > maxPluginMarkerBytes {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading plugin recovery marker: %w", err)
	}

	return string(data), nil
}

func (plugin pluginDirectoryInstall) backupProvenance() string {
	return managedMarker + ": backup v1 for " + filepath.Base(plugin.dir) + "\n"
}

func inspectBackupContainer(path string) (string, bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", false, fmt.Errorf("reading plugin backup container: %w", err)
	}

	var original string

	for _, entry := range entries {
		switch entry.Name() {
		case pluginBackupMarker:
		case pluginBackupOriginal:
			original = filepath.Join(path, pluginBackupOriginal)
		default:
			return "", false, nil
		}
	}

	return original, true, nil
}

func (plugin pluginDirectoryInstall) temporaryName(name string, kind string) bool {
	suffix, ok := strings.CutPrefix(name, "."+filepath.Base(plugin.dir)+"."+kind+"-")
	if !ok {
		return false
	}
	// MkdirTemp uses an unpadded decimal uint32, not an arbitrary wildcard.
	number, err := strconv.ParseUint(suffix, 10, 32)

	return err == nil && strconv.FormatUint(number, 10) == suffix
}

func (plugin pluginDirectoryInstall) ensureManaged(force bool) error {
	managed, err := plugin.managed()
	if err != nil {
		return err
	}

	if !managed && !force {
		return fmt.Errorf("%w: %s; pass --force to replace it", errForeignFile, plugin.dir)
	}

	return nil
}

func (plugin pluginDirectoryInstall) managed() (bool, error) {
	info, err := os.Stat(plugin.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}

		return false, fmt.Errorf("checking plugin directory: %w", err)
	}

	if !info.IsDir() {
		return false, nil
	}

	marker, err := os.ReadFile(filepath.Join(plugin.dir, harness.ArtifactMarkerFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, fmt.Errorf("reading plugin marker: %w", err)
	}

	return strings.Contains(string(marker), managedMarker), nil
}

func (plugin pluginDirectoryInstall) needsUpdate() (bool, error) {
	info, err := os.Stat(plugin.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}

		return false, fmt.Errorf("checking plugin directory %s: %w", plugin.dir, err)
	}

	if !info.IsDir() {
		return true, nil
	}

	for name, content := range plugin.files {
		path := filepath.Join(plugin.dir, name)

		current, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}

			return false, fmt.Errorf("reading plugin file %s: %w", path, err)
		}

		if string(current) != content {
			return true, nil
		}
	}

	return plugin.hasStaleFiles()
}

func (plugin pluginDirectoryInstall) stage() (string, error) {
	parent := filepath.Dir(plugin.dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("creating plugin parent directory: %w", err)
	}

	stagedDir, err := os.MkdirTemp(parent, "."+filepath.Base(plugin.dir)+".stage-*")
	if err != nil {
		return "", fmt.Errorf("creating staged plugin directory: %w", err)
	}

	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stagedDir)
		}
	}()

	// Put the managed identity on disk before writing other files so an
	// interrupted stage can be identified without trusting its directory name.
	if marker, ok := plugin.files[harness.ArtifactMarkerFile]; ok {
		if err := writeStagedPluginFile(
			filepath.Join(stagedDir, harness.ArtifactMarkerFile),
			[]byte(marker),
		); err != nil {
			return "", fmt.Errorf("writing staged plugin marker: %w", err)
		}
	}

	for name, content := range plugin.files {
		if name == harness.ArtifactMarkerFile {
			continue
		}

		path := filepath.Join(stagedDir, name)
		if err := writeStagedPluginFile(path, []byte(content)); err != nil {
			return "", fmt.Errorf("writing plugin file %s: %w", path, err)
		}
	}

	if err := harness.SyncDir(stagedDir); err != nil {
		return "", err
	}

	keep = true

	return stagedDir, nil
}

func writeStagedPluginFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating staged plugin file directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("opening staged plugin file: %w", err)
	}

	keepOpen := true
	defer func() {
		if keepOpen {
			_ = file.Close()
		}
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("writing staged plugin file: %w", err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("syncing staged plugin file: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("closing staged plugin file: %w", err)
	}

	keepOpen = false

	return nil
}

func (plugin pluginDirectoryInstall) hasStaleFiles() (bool, error) {
	expectedDirs := plugin.expectedDirs()

	err := filepath.WalkDir(plugin.dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if path == plugin.dir {
			return nil
		}

		rel, err := filepath.Rel(plugin.dir, path)
		if err != nil {
			return fmt.Errorf("relativizing plugin path: %w", err)
		}

		if entry.IsDir() {
			if _, ok := expectedDirs[rel]; ok {
				return nil
			}

			return errStalePluginFile
		}

		if _, ok := plugin.files[rel]; !ok {
			return errStalePluginFile
		}

		return nil
	})
	if err == nil {
		return false, nil
	}

	if errors.Is(err, errStalePluginFile) {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}

	return false, fmt.Errorf("walking plugin directory %s: %w", plugin.dir, err)
}

func (plugin pluginDirectoryInstall) expectedDirs() map[string]struct{} {
	dirs := make(map[string]struct{})

	for name := range plugin.files {
		dir := filepath.Dir(name)
		for dir != "." {
			dirs[dir] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}

	return dirs
}

func (plugin pluginDirectoryInstall) installStaged() (func() error, func() error, error) {
	stagedDir, err := plugin.stage()
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = os.RemoveAll(stagedDir)
	}()

	return plugin.replace(stagedDir)
}

// replace installs a fully staged plugin directory and returns
// rollback/commit callbacks so related manifest updates can stay all-or-restore
// at the install operation level.
func (plugin pluginDirectoryInstall) replace(stagedDir string) (func() error, func() error, error) {
	parent := filepath.Dir(plugin.dir)

	backup, backupExists, err := plugin.backupCurrent(parent)
	if err != nil {
		return nil, nil, err
	}

	if err := plugin.rename(stagedDir, plugin.dir); err != nil {
		cause := fmt.Errorf("installing staged plugin directory: %w", err)
		return nil, nil, errors.Join(cause, plugin.restoreBackup(backup, backupExists))
	}

	rollback := func() error {
		return plugin.rollbackReplacement(parent, backup, backupExists)
	}
	commit := func() error {
		return plugin.commitReplacement(parent, backup, backupExists)
	}

	if err := harness.SyncDir(parent); err != nil {
		return nil, nil, errors.Join(err, rollback())
	}

	return rollback, commit, nil
}

func (plugin pluginDirectoryInstall) backupCurrent(parent string) (string, bool, error) {
	if _, err := os.Lstat(plugin.dir); errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("checking plugin directory before backup: %w", err)
	}

	// Keep the MkdirTemp container. Recording ownership before the rename
	// lets recovery distinguish markerless originals from unrelated data.
	backup, err := os.MkdirTemp(parent, "."+filepath.Base(plugin.dir)+".backup-*")
	if err != nil {
		return "", false, fmt.Errorf("creating plugin backup container: %w", err)
	}

	if err := plugin.prepareBackup(parent, backup); err != nil {
		return "", false, errors.Join(err, plugin.removeAll(backup))
	}

	if err := plugin.rename(plugin.dir, filepath.Join(backup, pluginBackupOriginal)); err != nil {
		return "", false, errors.Join(
			fmt.Errorf("backing up plugin directory: %w", err),
			plugin.removeAll(backup),
		)
	}

	if err := errors.Join(harness.SyncDir(backup), harness.SyncDir(parent)); err != nil {
		return "", false, errors.Join(err, plugin.restoreBackup(backup, true))
	}

	return backup, true, nil
}

func (plugin pluginDirectoryInstall) prepareBackup(parent string, backup string) error {
	if err := writeStagedPluginFile(
		filepath.Join(backup, pluginBackupMarker),
		[]byte(plugin.backupProvenance()),
	); err != nil {
		return fmt.Errorf("writing plugin backup provenance: %w", err)
	}

	if err := harness.SyncDir(backup); err != nil {
		return err
	}

	return harness.SyncDir(parent)
}

func (plugin pluginDirectoryInstall) restoreBackup(backup string, backupExists bool) error {
	if !backupExists {
		return nil
	}

	original := filepath.Join(backup, pluginBackupOriginal)
	if err := plugin.rename(original, plugin.dir); err != nil {
		return fmt.Errorf("restoring plugin directory backup %s: %w", backup, err)
	}

	parent := filepath.Dir(plugin.dir)
	if err := errors.Join(harness.SyncDir(backup), harness.SyncDir(parent)); err != nil {
		return err
	}

	if err := plugin.removeAll(backup); err != nil {
		return err
	}

	return harness.SyncDir(parent)
}

func (plugin pluginDirectoryInstall) rollbackReplacement(
	parent string,
	backup string,
	backupExists bool,
) error {
	var removeErr error
	if err := plugin.removeAll(plugin.dir); err != nil {
		removeErr = fmt.Errorf("removing staged plugin directory: %w", err)
	}

	var restoreErr error
	if backupExists {
		restoreErr = plugin.restoreBackup(backup, backupExists)
	} else {
		restoreErr = harness.SyncDir(parent)
	}

	return errors.Join(removeErr, restoreErr)
}

func (plugin pluginDirectoryInstall) commitReplacement(
	parent string,
	backup string,
	backupExists bool,
) error {
	if backupExists {
		if err := plugin.removeAll(backup); err != nil {
			return fmt.Errorf("removing plugin directory backup %s: %w", backup, err)
		}
	}

	return harness.SyncDir(parent)
}

func (plugin pluginDirectoryInstall) snippet() string {
	parts := make([]string, 0, len(plugin.snippetOrder))
	for _, name := range plugin.snippetOrder {
		parts = append(parts, "== "+name+" ==\n"+plugin.files[name])
	}

	return strings.Join(parts, "\n")
}
