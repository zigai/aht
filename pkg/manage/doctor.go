package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/config"
	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	// DoctorStatusOK means the check passed.
	DoctorStatusOK DoctorStatus = "ok"

	// DoctorStatusInfo reports a neutral fact, such as an optional component that is absent.
	DoctorStatusInfo DoctorStatus = "info"

	// DoctorStatusWarn means aht works now but is degraded or likely to break.
	DoctorStatusWarn DoctorStatus = "warn"

	// DoctorStatusFail means the core path, or a feature the user set up, does not work.
	DoctorStatusFail DoctorStatus = "fail"

	// DoctorStatusSkip means the check did not run; Needs or the message says why.
	DoctorStatusSkip DoctorStatus = "skip"

	doctorGroupInstall      = "Install"
	doctorGroupConfig       = "Config"
	doctorGroupObserver     = "Observer"
	doctorGroupDetection    = "Detection"
	doctorGroupStore        = "Store"
	doctorGroupTracker      = "Tracker"
	doctorGroupHistory      = "History"
	doctorGroupIntegrations = "Integrations"

	doctorCheckConfig   = "config.file"
	doctorCheckPlatform = "observer.platform"
	doctorCheckService  = "tracker.service"

	doctorCoreCheckCount = 9
	executableName       = "aht"
	trackerEnableCommand = "aht manage tracker enable"
	trackerStatusCommand = "aht manage tracker status"
	supportedPlatforms   = "aht runs on Linux and macOS"
	issuesURL            = "https://github.com/zigai/aht/issues"
)

// ErrNoRepair means a doctor check has no automatic repair.
var ErrNoRepair = errors.New("doctor check has no automatic repair")

type (
	// DoctorStatus is the outcome of one doctor check.
	DoctorStatus string

	// DoctorCheck is one diagnostic row. ID is "<group>.<name>" with the group
	// lowercased, and stays stable across releases.
	DoctorCheck struct {
		ID      string       `json:"id"`
		Group   string       `json:"group"`
		Status  DoctorStatus `json:"status"`
		Message string       `json:"message"`
		// Details lists one finding per entry when a check has several.
		Details []string `json:"details,omitempty"`
		// Needs names the check whose result caused this one to be skipped.
		Needs string     `json:"needs,omitempty"`
		Fix   *DoctorFix `json:"fix"`

		repair func(context.Context) (string, error)
	}

	// DoctorFix says how to resolve a warn or fail check. Manual, when set, is
	// the full instruction and includes Command.
	DoctorFix struct {
		Command string `json:"command,omitempty"`
		Manual  string `json:"manual,omitempty"`
		// Automatic reports whether [DoctorCheck.Repair] applies the fix.
		Automatic bool `json:"automatic"`
	}

	// DoctorSummary counts checks by status.
	DoctorSummary struct {
		OK   int `json:"ok"`
		Info int `json:"info"`
		Warn int `json:"warn"`
		Fail int `json:"fail"`
		Skip int `json:"skip"`
	}

	// DoctorResult is the complete diagnostic report, in check order.
	DoctorResult struct {
		Checks  []DoctorCheck `json:"checks"`
		Summary DoctorSummary `json:"summary"`
	}

	// DoctorOptions controls what Doctor evaluates.
	DoctorOptions struct {
		// IncludeAll also reports harnesses whose integration is not installed
		// or needs no setup.
		IncludeAll bool
		// ConfigPath selects the config file, as --config does.
		ConfigPath string
		// NoConfig skips configuration files, as --no-config does. Checks that
		// need configured values are then skipped.
		NoConfig bool
		// MaxHealthAge is the age after which tracker health is stale. Zero
		// derives it from the tracker interval.
		MaxHealthAge time.Duration
	}

	executableCopy struct {
		path     string
		resolved string
	}

	configState struct {
		cfg   config.Config
		ready bool
	}
)

// Doctor diagnoses the installation, configuration, observer, detection
// manifests, session store, history index, tracker, and harness integrations.
// It changes nothing on disk; checks that cannot run are reported as skipped.
func (m *Manager) Doctor(ctx context.Context, opts DoctorOptions) DoctorResult {
	copies := executableCopies()
	configCheck, cfg := checkConfigFile(opts)
	platform := checkPlatform()
	serviceCheck := m.checkService(ctx, cfg, platform)
	checks := make([]DoctorCheck, 0, doctorCoreCheckCount+len(catalog.All()))
	checks = append(checks,
		m.checkInstallPath(copies),
		configCheck,
		platform,
		checkProcessEnumeration(ctx),
		checkManifests(cfg),
		m.checkStore(ctx),
		serviceCheck,
		m.checkHealth(opts.MaxHealthAge, serviceCheck),
		checkHistoryRefresh(ctx),
	)
	checks = append(checks, m.checkIntegrations(ctx, opts.IncludeAll, copies)...)
	return DoctorResult{Checks: checks, Summary: summarizeDoctorChecks(checks)}
}

// Failed reports whether any check failed.
func (r DoctorResult) Failed() bool {
	return r.Summary.Fail > 0
}

// Name returns the check ID without its group prefix.
func (c DoctorCheck) Name() string {
	return strings.TrimPrefix(c.ID, strings.ToLower(c.Group)+".")
}

// Repair applies the check's automatic fix and describes what it did. It
// returns [ErrNoRepair] when Fix is not automatic.
func (c DoctorCheck) Repair(ctx context.Context) (string, error) {
	if c.repair == nil {
		return "", fmt.Errorf("%w: %s", ErrNoRepair, c.ID)
	}
	return c.repair(ctx)
}

func checkHistoryRefresh(ctx context.Context) DoctorCheck {
	check := newDoctorCheck(doctorGroupHistory, "refresh")
	var historyCatalog history.Catalog
	status, err := historyCatalog.RefreshStatus(ctx)
	if err != nil {
		check.Status, check.Message = DoctorStatusWarn, sentence(err.Error())
	} else {
		coverage := "complete"
		if status.Incomplete {
			check.Status = DoctorStatusWarn
			coverage = "partial"
		}
		check.Message = fmt.Sprintf("Last refresh %s (%s; issues=%d)", status.CompletedAt.Format(time.RFC3339Nano), coverage, status.IssueCount)
		for _, source := range status.Refresh {
			check.Details = append(check.Details, fmt.Sprintf("%s %s: checked=%d unchanged=%d skipped=%d resumed=%d reparsed=%d bytes_parsed=%d parse_time=%s store_time=%s reasons=%v", source.Source.Harness, source.Source.Path, source.Checked, source.Unchanged, source.Skipped, source.Resumed, source.Reparsed, source.BytesParsed, source.ParseTime, source.StoreTime, source.Reasons))
		}
	}
	if check.Status == DoctorStatusWarn {
		command := "aht manage history refresh"
		check.Fix = &DoctorFix{Command: command, Manual: "Refresh the history index: " + command, Automatic: false}
	}
	return check
}

func (m *Manager) checkInstallPath(copies []executableCopy) DoctorCheck {
	check := newDoctorCheck(doctorGroupInstall, "path")
	switch len(copies) {
	case 0:
		check.Status, check.Message = DoctorStatusInfo, executableName+" is not on PATH"
	case 1:
		check.Status, check.Message = DoctorStatusOK, copies[0].path+", no other copies on PATH"
	default:
		self := resolvedExecutable(m.config.Binary)
		check.Status, check.Message = DoctorStatusWarn, strconv.Itoa(len(copies))+" copies on PATH"
		for _, executable := range copies {
			detail := executable.path
			if executable.resolved == self {
				detail += " (this executable)"
			}
			check.Details = append(check.Details, detail)
		}
		check.Fix = manualFix("Remove the copies you do not use; the first one listed is the one your shell runs")
	}
	return check
}

func (m *Manager) checkStore(ctx context.Context) DoctorCheck {
	check := newDoctorCheck(doctorGroupStore, "read")
	sessions, err := registry.NewJournal(m.config.StorePath, catalog.Rules{}).Verify(ctx)
	if err == nil {
		check.Status = DoctorStatusOK
		check.Message = fmt.Sprintf("%s, schema version %d", plural(sessions, "session"), registry.StoreSchemaVersion)
		return check
	}
	check.Status = DoctorStatusFail
	check.Message = sentence(err.Error())
	if unsupported, ok := errors.AsType[*registry.UnsupportedSchemaError](err); ok {
		version := "a missing"
		if unsupported.Version != 0 {
			version = "version " + strconv.Itoa(unsupported.Version)
		}
		check.Message = fmt.Sprintf("Store has %s schema; this aht reads version %d", version, registry.StoreSchemaVersion)
	}
	reset := "aht manage state reset --force"
	if m.config.StorePath != registry.DefaultStorePath() {
		reset = "aht --store " + m.config.StorePath + " manage state reset --force"
	}
	check.Fix = &DoctorFix{
		Command:   reset,
		Manual:    "Reset the store, which deletes recorded sessions: " + reset,
		Automatic: false,
	}
	return check
}

func (m *Manager) checkService(ctx context.Context, cfg configState, platform DoctorCheck) DoctorCheck {
	check := newDoctorCheck(doctorGroupTracker, "service")
	if platform.Status == DoctorStatusFail {
		return skipDoctorCheck(check, doctorCheckPlatform)
	}
	if !cfg.ready {
		return skipDoctorCheck(check, doctorCheckConfig)
	}
	status, err := m.TrackerStatus(ctx)
	switch {
	case errors.Is(err, ErrForeignTracker):
		check.Status, check.Message = DoctorStatusFail, sentence(err.Error())
		check.Fix = &DoctorFix{
			Command:   trackerEnableCommand,
			Manual:    "Move the existing service definition aside, then run: " + trackerEnableCommand,
			Automatic: false,
		}
	case err != nil:
		check.Status, check.Message = DoctorStatusFail, sentence(err.Error())
		check.Fix = &DoctorFix{
			Command:   trackerStatusCommand,
			Manual:    "Inspect the service manager's report: " + trackerStatusCommand,
			Automatic: false,
		}
	case !status.Installed, !status.Current, !status.Running:
		check.Status, check.Message = DoctorStatusWarn, trackerProblem(status)
		check.Fix = &DoctorFix{Command: trackerEnableCommand, Manual: "", Automatic: true}
		check.repair = func(ctx context.Context) (string, error) {
			result, err := m.EnableTracker(ctx, TrackerOptions{DryRun: false})
			return result.Message, err
		}
	default:
		check.Status, check.Message = DoctorStatusOK, "Running"
		if status.Manager != "" {
			check.Message += " under " + status.Manager
		}
	}
	return check
}

func (m *Manager) checkHealth(maxAge time.Duration, serviceCheck DoctorCheck) DoctorCheck {
	check := newDoctorCheck(doctorGroupTracker, "health")
	if serviceCheck.Status == DoctorStatusWarn || serviceCheck.Status == DoctorStatusFail {
		return skipDoctorCheck(check, doctorCheckService)
	}
	path := m.HealthPath()
	health, err := ReadTrackerHealth(path, time.Now().UTC(), maxAge)
	waitFix := &DoctorFix{
		Command:   trackerStatusCommand,
		Manual:    "Wait one tracker cycle; if this persists, inspect the tracker: " + trackerStatusCommand,
		Automatic: false,
	}
	switch {
	case err == nil:
		check.Status, check.Message = DoctorStatusOK, sentence(health.Message)
	case errors.Is(err, ErrHealthMissing):
		check.Status, check.Message, check.Fix = DoctorStatusWarn, "No health record yet", waitFix
	case errors.Is(err, ErrReconciliationIncomplete):
		check.Status, check.Message, check.Fix = DoctorStatusWarn, "No successful reconciliation yet", waitFix
	case errors.Is(err, ErrHealthStale), errors.Is(err, ErrHealthDegraded):
		check.Status, check.Message = DoctorStatusWarn, sentence(health.Message)
		check.Fix = &DoctorFix{
			Command:   trackerStatusCommand,
			Manual:    "Inspect the tracker: " + trackerStatusCommand,
			Automatic: false,
		}
	default:
		check.Status, check.Message = DoctorStatusFail, "Health record is unreadable"
		check.Details = []string{sentence(health.Message)}
		check.Fix = &DoctorFix{
			Command:   "rm " + path,
			Manual:    "Delete the record; the tracker rewrites it on its next cycle: rm " + path,
			Automatic: false,
		}
	}
	return check
}

func (m *Manager) checkIntegrations(ctx context.Context, includeAll bool, copies []executableCopy) []DoctorCheck {
	var checks []DoctorCheck
	for _, adapter := range catalog.All() {
		id := adapter.Definition().ID
		check := newDoctorCheck(doctorGroupIntegrations, string(id))
		if _, ok := adapter.(harnesspkg.NoInstallRequired); ok {
			if includeAll {
				check.Status, check.Message = DoctorStatusInfo, "No setup required"
				checks = append(checks, check)
			}
			continue
		}
		status, err := m.IntegrationStatus(ctx, id)
		if err != nil {
			check.Status, check.Message = DoctorStatusFail, sentence(err.Error())
			check.Fix = manualFix("Report this at " + issuesURL)
			checks = append(checks, check)
			continue
		}
		switch status.Status {
		case ArtifactCurrent:
			check.Status, check.Message = DoctorStatusOK, "Current"
		case ArtifactMissing:
			if !includeAll {
				continue
			}
			check.Status, check.Message = DoctorStatusInfo, "Not installed"
		case ArtifactStale:
			m.describeStaleIntegration(ctx, &check, status, copies)
		case ArtifactForeign:
			command := fmt.Sprintf("aht manage integrations install %s --force", id)
			check.Status, check.Message = DoctorStatusFail, sentence(status.Message)
			check.Fix = &DoctorFix{
				Command:   command,
				Manual:    "Back up the file, then let aht replace it: " + command,
				Automatic: false,
			}
		}
		checks = append(checks, check)
	}
	return checks
}

func (m *Manager) describeStaleIntegration(
	ctx context.Context,
	check *DoctorCheck,
	status IntegrationStatus,
	copies []executableCopy,
) {
	self := resolvedExecutable(m.config.Binary)
	for _, executable := range copies {
		if executable.resolved == self {
			continue
		}
		for _, binary := range slices.Compact([]string{executable.path, executable.resolved}) {
			other, err := install.Inspect(ctx, status.Harness, binary)
			if err == nil && other.Status == install.ArtifactCurrent {
				check.Status = DoctorStatusInfo
				check.Message = "Current for " + executable.path + ", which is not this executable"
				return
			}
		}
	}
	check.Status, check.Message = DoctorStatusWarn, sentence(status.Message)
	check.Fix = &DoctorFix{Command: "aht manage upgrade", Manual: sentence(status.NextStep), Automatic: true}
	binary := m.config.Binary
	check.repair = func(ctx context.Context) (string, error) {
		results, err := install.UpgradeHarness(ctx, status.Harness, binary, false)
		messages := make([]string, 0, len(results))
		for _, result := range results {
			messages = append(messages, result.Message)
		}
		return strings.Join(messages, "; "), err
	}
}

func newDoctorCheck(group, name string) DoctorCheck {
	return DoctorCheck{
		ID:      strings.ToLower(group) + "." + name,
		Group:   group,
		Status:  DoctorStatusOK,
		Message: "",
		Details: nil,
		Needs:   "",
		Fix:     nil,
		repair:  nil,
	}
}

func skipDoctorCheck(check DoctorCheck, needs string) DoctorCheck {
	check.Status, check.Message, check.Needs = DoctorStatusSkip, "needs "+needs, needs
	return check
}

func manualFix(manual string) *DoctorFix {
	return &DoctorFix{Command: "", Manual: manual, Automatic: false}
}

func checkConfigFile(opts DoctorOptions) (DoctorCheck, configState) {
	check := newDoctorCheck(doctorGroupConfig, "file")
	if opts.NoConfig {
		check.Status, check.Message = DoctorStatusSkip, "--no-config"
		return check, configState{cfg: config.Defaults(), ready: false}
	}
	cfg, meta, resolved, err := config.LoadWithMetadata(config.Options{
		Path:     opts.ConfigPath,
		Explicit: opts.ConfigPath != "",
		NoConfig: false,
		Stdin:    nil,
	})
	if err != nil {
		describeConfigError(&check, opts.ConfigPath, resolved, err)
		return check, configState{cfg: config.Defaults(), ready: false}
	}
	switch active := meta.ActiveFiles(); len(active) {
	case 0:
		check.Message = "No config file; using defaults"
	case 1:
		check.Message = active[0] + " is valid"
	default:
		check.Message = strconv.Itoa(len(active)) + " config files are valid"
		check.Details = active
	}
	return check, configState{cfg: cfg, ready: true}
}

func describeConfigError(check *DoctorCheck, explicit, resolved string, err error) {
	check.Status = DoctorStatusFail
	if problems := configErrors(err); len(problems) > 0 {
		lines := make([]string, 0, len(problems))
		for _, problem := range problems {
			lines = append(lines, sentence(problem.Err.Error())+" at "+originLocation(problem.Origin))
		}
		check.Message = lines[0]
		if len(lines) > 1 {
			check.Message, check.Details = plural(len(lines), "invalid setting"), lines
		}
		check.Fix = manualFix("Correct the listed settings")
		return
	}
	target := explicit
	if target == "" {
		target = strings.TrimSpace(os.Getenv(config.ConfigEnv))
	}
	switch {
	case errors.Is(err, config.ErrConfigNotFound):
		check.Message = target + " does not exist"
		check.Fix = manualFix("Point --config or " + config.ConfigEnv + " at an existing file, or create it with: aht --config " + target + " manage config init")
	case errors.Is(err, config.ErrConfigIsDirectory):
		check.Message = target + " is a directory"
		check.Fix = manualFix("Point --config or " + config.ConfigEnv + " at a TOML file")
	case errors.Is(err, config.ErrConfigFileTooLarge):
		check.Message = resolved + " is larger than 1 MiB"
		check.Fix = manualFix("Shrink the config file")
	default:
		check.Message = "Cannot load " + resolved
		check.Details = []string{sentence(innermostError(err).Error())}
		check.Fix = manualFix("Correct the file so it is valid TOML")
	}
}

func configErrors(err error) []*strata.ConfigError {
	if problem, ok := err.(*strata.ConfigError); ok { //nolint:errorlint // Walks the tree itself so joined errors yield every problem.
		return []*strata.ConfigError{problem}
	}
	switch wrapped := err.(type) { //nolint:errorlint // Walks the tree itself so joined errors yield every problem.
	case interface{ Unwrap() []error }:
		var problems []*strata.ConfigError
		for _, child := range wrapped.Unwrap() {
			problems = append(problems, configErrors(child)...)
		}
		return problems
	case interface{ Unwrap() error }:
		return configErrors(wrapped.Unwrap())
	default:
		return nil
	}
}

func originLocation(origin strata.Origin) string {
	if origin.Line > 0 {
		return origin.Path + ":" + strconv.Itoa(origin.Line)
	}
	if origin.Path != "" {
		return origin.Path
	}
	return string(origin.Source)
}

func innermostError(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

func checkPlatform() DoctorCheck {
	check := newDoctorCheck(doctorGroupObserver, "platform")
	target := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		check.Status, check.Message = DoctorStatusFail, target+" is not supported"
		check.Fix = manualFix("Use Linux or macOS; " + supportedPlatforms)
		return check
	}
	check.Message = target
	return check
}

func checkProcessEnumeration(ctx context.Context) DoctorCheck {
	check := newDoctorCheck(doctorGroupObserver, "process-enumeration")
	_, err := processinfo.List(ctx)
	if err == nil {
		check.Message = "Complete process inventory for the current user"
		return check
	}
	check.Status, check.Message = DoctorStatusFail, sentence(err.Error())
	if permission, ok := errors.AsType[*processinfo.PermissionError](err); ok {
		check.Fix = manualFix("Give this user read access to " + permission.Path)
	} else if _, ok := errors.AsType[*processinfo.UnsupportedError](err); ok {
		check.Fix = manualFix("Use Linux or macOS; " + supportedPlatforms)
	} else {
		check.Fix = manualFix("Report this at " + issuesURL)
	}
	return check
}

func checkManifests(cfg configState) DoctorCheck {
	check := newDoctorCheck(doctorGroupDetection, "manifests")
	if !cfg.ready {
		return skipDoctorCheck(check, doctorCheckConfig)
	}
	dir := cfg.cfg.Detection.ManifestsDir
	if dir == "" {
		dir = agentstate.DefaultConfigDir()
	}
	loader := agentstate.Loader{ConfigDir: dir}
	var failures, warnings []string
	for _, harnessID := range catalog.Harnesses() {
		if !loader.Supports(harnessID) {
			continue
		}
		manifest, err := loader.Load(harnessID)
		switch {
		case err != nil:
			failures = append(failures, sentence(err.Error()))
		case manifest.Warning != "":
			warnings = append(warnings, sentence(manifest.Warning))
		}
	}
	findings := slices.Concat(failures, warnings)
	switch {
	case len(failures) > 0:
		check.Status = DoctorStatusFail
	case len(warnings) > 0:
		check.Status = DoctorStatusWarn
	default:
		check.Message = "Bundled and local manifests are valid"
		return check
	}
	check.Message = findings[0]
	if len(findings) > 1 {
		check.Message, check.Details = plural(len(findings), "manifest problem"), findings
	}
	check.Fix = manualFix("Fix or remove the listed manifests in " + dir)
	return check
}

func trackerProblem(status TrackerResult) string {
	switch {
	case !status.Installed:
		return "Not installed"
	case !status.Current:
		return "Installed service is out of date"
	default:
		return "Installed but stopped"
	}
}

func executableCopies() []executableCopy {
	var copies []executableCopy
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		path, err := exec.LookPath(filepath.Join(dir, executableName))
		if err != nil {
			continue
		}
		resolved := resolvedExecutable(path)
		if slices.ContainsFunc(copies, func(other executableCopy) bool { return other.resolved == resolved }) {
			continue
		}
		copies = append(copies, executableCopy{path: path, resolved: resolved})
	}
	return copies
}

func resolvedExecutable(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

func summarizeDoctorChecks(checks []DoctorCheck) DoctorSummary {
	var summary DoctorSummary
	for _, check := range checks {
		switch check.Status {
		case DoctorStatusOK:
			summary.OK++
		case DoctorStatusInfo:
			summary.Info++
		case DoctorStatusWarn:
			summary.Warn++
		case DoctorStatusFail:
			summary.Fail++
		case DoctorStatusSkip:
			summary.Skip++
		}
	}
	return summary
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}

func sentence(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if first == utf8.RuneError {
		return text
	}
	return string(unicode.ToUpper(first)) + text[size:]
}
