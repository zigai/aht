package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	doctorStatusWidth    = 4
	doctorIndent         = "  "
	doctorMinWrapWidth   = 20
	doctorFixCommandHint = " (or: aht doctor --fix)"

	doctorConfigLoaded    = "loaded"
	doctorConfigNotFound  = "not_found"
	doctorConfigNotLoaded = "not_loaded"
	doctorConfigSkipped   = "skipped"

	colorReset = "\x1b[0m"
)

var (
	errDoctorYesWithoutFix = errors.New("--yes requires --fix")

	doctorStatusColors = map[manage.DoctorStatus]string{
		manage.DoctorStatusOK:   "\x1b[32m",
		manage.DoctorStatusInfo: "\x1b[94m",
		manage.DoctorStatusWarn: "\x1b[33m",
		manage.DoctorStatusFail: "\x1b[31m",
		manage.DoctorStatusSkip: "\x1b[90m",
	}
)

type doctorReport struct {
	Version string               `json:"version"`
	Build   doctorBuild          `json:"build"`
	Install doctorInstall        `json:"install"`
	Paths   doctorPaths          `json:"paths"`
	Env     map[string]string    `json:"env"`
	Checks  []manage.DoctorCheck `json:"checks"`
	Summary manage.DoctorSummary `json:"summary"`
	Fixes   []doctorFixResult    `json:"fixes,omitempty"`
}

type doctorBuild struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
	Target string `json:"target"`
}

type doctorInstall struct {
	Executable string `json:"executable"`
	Method     string `json:"method,omitempty"`
}

type doctorPaths struct {
	Config []doctorConfigFile `json:"config"`
	State  doctorPath         `json:"state"`
	Store  doctorPath         `json:"store"`
}

type doctorPath struct {
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
}

type doctorConfigFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

type doctorFixResult struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Result string `json:"result"`
}

type doctorHeaderField struct {
	label  string
	values []string
}

type doctorLayout struct {
	nameWidth int
	width     int
	color     bool
}

func (app *application) newDoctorCommand() *cobra.Command {
	var verbose, fix, yes bool
	command := &cobra.Command{
		Use:           "doctor",
		Short:         "Check whether aht is set up and working",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if yes && !fix {
				return exitCode(errDoctorYesWithoutFix, exitCodeUsage)
			}
			manager := app.doctorManager(cmd)
			opts := manage.DoctorOptions{IncludeAll: verbose, ConfigPath: app.configPath, NoConfig: app.noConfig, MaxHealthAge: 0}
			result := manager.Doctor(cmd.Context(), opts)
			var fixes []doctorFixResult
			if fix {
				var err error
				if fixes, err = app.applyDoctorFixes(cmd.Context(), result, yes); err != nil {
					return err
				}
				if len(fixes) > 0 {
					result = manager.Doctor(cmd.Context(), opts)
				}
			}
			if err := app.writeDoctorReport(app.newDoctorReport(result, fixes)); err != nil {
				return err
			}
			if pending := automaticDoctorFixes(result); !fix && pending > 0 {
				app.warnf("Run 'aht doctor --fix' to apply %s.\n", countNoun(pending, "fix", "fixes"))
			}
			if result.Failed() {
				return exitCode(errSilentDoctorFailure, exitCodeGeneral)
			}
			return nil
		},
	}
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "include integrations that are not installed or need no setup")
	command.Flags().BoolVar(&fix, "fix", false, "apply automatic fixes, then check again")
	command.Flags().BoolVarP(&yes, "yes", "y", false, "apply fixes without confirmation prompts")
	return command
}

func (app *application) doctorManager(cmd *cobra.Command) *manage.Manager {
	opts, err := app.configuredServiceOptions(cmd, serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval})
	if err != nil {
		// The config.file check reports the failure and tracker.service is
		// skipped, so these defaults are never compared against the service.
		opts = service.Options{Binary: defaultInstallBinary(), StorePath: app.resolvedStorePath(), Interval: serviceDefaultInterval}
	}
	return manage.New(manage.Config{Binary: opts.Binary, StorePath: opts.StorePath, TrackerInterval: opts.Interval, TrackerGracePeriod: opts.GracePeriod})
}

func (app *application) applyDoctorFixes(ctx context.Context, result manage.DoctorResult, yes bool) ([]doctorFixResult, error) {
	var fixes []doctorFixResult
	for _, check := range result.Checks {
		if !hasAutomaticFix(check) {
			continue
		}
		action := check.Fix.Command
		if !yes {
			confirmed, err := app.confirmAction(ctx, nil, fmt.Sprintf("Fix %s by running '%s'? [y/N]: ", check.ID, action), "--yes")
			if err != nil {
				return fixes, err
			}
			if !confirmed {
				fixes = append(fixes, doctorFixResult{ID: check.ID, Action: action, Result: "declined"})
				continue
			}
		}
		app.warnf("Fixing %s: %s\n", check.ID, action)
		outcome := "applied"
		message, err := check.Repair(ctx)
		switch {
		case err != nil:
			outcome = "failed: " + err.Error()
		case message != "":
			outcome = "applied: " + message
		}
		app.warnf("%s%s\n", doctorIndent, outcome)
		fixes = append(fixes, doctorFixResult{ID: check.ID, Action: action, Result: outcome})
	}
	return fixes, nil
}

func (app *application) newDoctorReport(result manage.DoctorResult, fixes []doctorFixResult) doctorReport {
	home := homeDir()
	executable := defaultInstallBinary()
	report := doctorReport{
		Version: version,
		Build:   doctorBuild{Commit: commit, Date: date, Target: runtime.GOOS + "/" + runtime.GOARCH},
		Install: doctorInstall{Executable: abbreviateHome(executable, home), Method: installMethod(executable)},
		Paths: doctorPaths{
			Config: app.doctorConfigFiles(home),
			State:  doctorPath{Path: abbreviateHome(registry.DefaultStateDir(), home), Source: firstSetEnv(registry.StateDirEnv, "XDG_STATE_HOME")},
			Store:  doctorPath{Path: abbreviateHome(app.resolvedStorePath(), home), Source: app.storeSource()},
		},
		Env:     doctorEnv(home),
		Checks:  make([]manage.DoctorCheck, 0, len(result.Checks)),
		Summary: result.Summary,
		Fixes:   fixes,
	}
	for _, check := range result.Checks {
		check.Message = abbreviateHome(check.Message, home)
		details := make([]string, 0, len(check.Details))
		for _, detail := range check.Details {
			details = append(details, abbreviateHome(detail, home))
		}
		check.Details = slices.Clip(details)
		if check.Fix != nil {
			fix := *check.Fix
			fix.Command, fix.Manual = abbreviateHome(fix.Command, home), abbreviateHome(fix.Manual, home)
			check.Fix = &fix
		}
		report.Checks = append(report.Checks, check)
	}
	return report
}

func (app *application) doctorConfigFiles(home string) []doctorConfigFile {
	explicit, source := app.configPath, "--config"
	if explicit == "" {
		explicit, source = strings.TrimSpace(os.Getenv(config.ConfigEnv)), config.ConfigEnv
	}
	user := filepath.Join(config.UserConfigDir(), "aht", "config.toml")
	_, loadErr := app.loadConfig()
	active := app.cfgMeta.ActiveFiles()
	status := func(path string) string {
		return app.doctorConfigStatus(path, active, loadErr)
	}
	var files []doctorConfigFile
	// Strata does not expose the system candidates it probed, only the files it loaded.
	for _, path := range active {
		if path != user && path != explicit {
			files = append(files, doctorConfigFile{Path: abbreviateHome(path, home), Status: doctorConfigLoaded, Source: ""})
		}
	}
	if user != explicit {
		files = append(files, doctorConfigFile{Path: abbreviateHome(user, home), Status: status(user), Source: ""})
	}
	if explicit != "" {
		files = append(files, doctorConfigFile{Path: abbreviateHome(explicit, home), Status: status(explicit), Source: source})
	}
	return files
}

func (app *application) doctorConfigStatus(path string, active []string, loadErr error) string {
	switch {
	case app.noConfig:
		return doctorConfigSkipped
	case loadErr == nil && slices.Contains(active, path):
		return doctorConfigLoaded
	case loadErr == nil:
		return doctorConfigNotFound
	}
	// After a failed load, only the filesystem can tell an absent candidate from
	// one that exists but was not loaded. The path is the user's own config
	// location from flags or environment, stat'ed with that user's privileges,
	// so no trusted root exists to confine it to and no privilege boundary is crossed.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) { //nolint:gosec // G703: see comment above.
		return doctorConfigNotFound
	}
	return doctorConfigNotLoaded
}

func (app *application) storeSource() string {
	if app.storePath != "" {
		return "--store"
	}
	return firstSetEnv(registry.StorePathEnv, registry.StateDirEnv, "XDG_STATE_HOME")
}

func (app *application) writeDoctorReport(report doctorReport) error {
	if app.outputJSON {
		return app.writeJSON(report)
	}
	var out strings.Builder
	writeDoctorHeader(&out, report)
	layout := doctorLayout{nameWidth: 0, width: terminalWidth(app.stdout), color: app.isColorEnabled()}
	for _, check := range report.Checks {
		layout.nameWidth = max(layout.nameWidth, len(check.Name()))
	}
	group := ""
	for _, check := range report.Checks {
		if check.Group != group {
			group = check.Group
			out.WriteString("\n" + group + "\n")
		}
		layout.writeCheck(&out, check)
	}
	out.WriteString("\n" + doctorVerdict(report.Summary) + "\n")
	s := report.Summary
	fmt.Fprintf(&out, "%d ok, %d info, %d warn, %d fail, %d skip\n", s.OK, s.Info, s.Warn, s.Fail, s.Skip)
	return app.writef("%s", out.String())
}

func (layout doctorLayout) writeCheck(out *strings.Builder, check manage.DoctorCheck) {
	status := string(check.Status)
	padding := strings.Repeat(" ", doctorStatusWidth-len(status))
	if color, ok := doctorStatusColors[check.Status]; ok && layout.color {
		status = color + status + colorReset
	}
	nameColumn := len(doctorIndent) + doctorStatusWidth + len(doctorIndent)
	messageColumn := nameColumn + layout.nameWidth + len(doctorIndent)
	name := check.Name() + strings.Repeat(" ", layout.nameWidth-len(check.Name()))
	out.WriteString(doctorIndent + status + padding + doctorIndent + name + doctorIndent)
	layout.writeWrapped(out, check.Message, messageColumn, false)
	for _, detail := range check.Details {
		layout.writeWrapped(out, detail, messageColumn, true)
	}
	if check.Fix == nil {
		return
	}
	text := check.Fix.Manual
	if text == "" {
		text = check.Fix.Command
	}
	if check.Fix.Automatic {
		text += doctorFixCommandHint
	}
	const fixLabel = "fix: "
	out.WriteString(strings.Repeat(" ", nameColumn) + fixLabel)
	layout.writeWrapped(out, text, nameColumn+len(fixLabel), false)
}

func (layout doctorLayout) writeWrapped(out *strings.Builder, text string, column int, indentFirst bool) {
	lines := []string{sanitizeHumanText(text)}
	if layout.width > 0 {
		lines = wrapHumanText(text, max(doctorMinWrapWidth, layout.width-column))
	}
	for index, line := range lines {
		if index > 0 || indentFirst {
			out.WriteString(strings.Repeat(" ", column))
		}
		out.WriteString(line + "\n")
	}
}

func writeDoctorHeader(out *strings.Builder, report doctorReport) {
	out.WriteString(doctorIdentity(report) + "\n")
	const labelWidth = len("executable")
	for _, field := range doctorHeaderFields(report) {
		for index, value := range field.values {
			label := ""
			if index == 0 {
				label = field.label
			}
			fmt.Fprintf(out, "%s%-*s%s%s\n", doctorIndent, labelWidth, label, doctorIndent, value)
		}
	}
}

func doctorIdentity(report doctorReport) string {
	identity := "aht " + report.Version
	var build []string
	if report.Build.Commit != "" && report.Build.Commit != "none" {
		build = append(build, report.Build.Commit)
	}
	if report.Build.Date != "" && report.Build.Date != "unknown" {
		build = append(build, report.Build.Date)
	}
	if len(build) > 0 {
		identity += " (" + strings.Join(build, ", ") + ")"
	}
	return identity + " " + report.Build.Target
}

func doctorHeaderFields(report doctorReport) []doctorHeaderField {
	executable := report.Install.Executable
	if report.Install.Method != "" {
		executable += " (" + report.Install.Method + ")"
	}
	configs := make([]string, 0, len(report.Paths.Config))
	for _, file := range report.Paths.Config {
		configs = append(configs, file.Path+" ("+describeDoctorConfigStatus(file)+")")
	}
	env := make([]string, 0, len(report.Env))
	for _, name := range slices.Sorted(maps.Keys(report.Env)) {
		env = append(env, name+"="+report.Env[name])
	}
	return []doctorHeaderField{
		{label: "executable", values: []string{executable}},
		{label: "config", values: configs},
		{label: "state", values: []string{describeDoctorPath(report.Paths.State)}},
		{label: "store", values: []string{describeDoctorPath(report.Paths.Store)}},
		{label: "env", values: env},
	}
}

func describeDoctorConfigStatus(file doctorConfigFile) string {
	status := strings.ReplaceAll(file.Status, "_", " ")
	switch {
	case file.Status == doctorConfigSkipped:
		return status + " by --no-config"
	case file.Source != "":
		return status + ", via " + file.Source
	default:
		return status
	}
}

func describeDoctorPath(path doctorPath) string {
	if path.Source == "" {
		return path.Path
	}
	return path.Path + " (via " + path.Source + ")"
}

func doctorVerdict(summary manage.DoctorSummary) string {
	switch {
	case summary.Fail == 0 && summary.Warn == 0:
		return "No problems found."
	case summary.Fail == 0:
		return "No failures; " + countNoun(summary.Warn, "warning", "warnings") + "."
	case summary.Warn == 0 && summary.Fail == 1:
		return "1 failure needs attention."
	case summary.Warn == 0:
		return countNoun(summary.Fail, "failure", "failures") + " need attention."
	default:
		return countNoun(summary.Fail, "failure", "failures") + " and " + countNoun(summary.Warn, "warning", "warnings") + " need attention."
	}
}

func automaticDoctorFixes(result manage.DoctorResult) int {
	count := 0
	for _, check := range result.Checks {
		if hasAutomaticFix(check) {
			count++
		}
	}
	return count
}

func hasAutomaticFix(check manage.DoctorCheck) bool {
	return check.Fix != nil && check.Fix.Automatic &&
		(check.Status == manage.DoctorStatusWarn || check.Status == manage.DoctorStatusFail)
}

func doctorEnv(home string) map[string]string {
	names := []string{config.ConfigEnv, registry.StorePathEnv, registry.StateDirEnv, broker.SocketPathEnv, "XDG_CONFIG_HOME", "XDG_STATE_HOME"}
	secrets := map[string]bool{}
	for _, variable := range strata.EnvVars[config.Config]("AHT_") {
		for _, name := range variable.Names {
			names = append(names, name)
			secrets[name] = variable.IsSecret
		}
	}
	env := make(map[string]string)
	for _, name := range names {
		value := os.Getenv(name)
		switch {
		case strings.TrimSpace(value) == "":
		case secrets[name]:
			env[name] = "set"
		default:
			env[name] = abbreviateHome(value, home)
		}
	}
	return env
}

func firstSetEnv(names ...string) string {
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return name
		}
	}
	return ""
}

func installMethod(executable string) string {
	gobin := os.Getenv("GOBIN")
	if gobin == "" {
		gopath := filepath.SplitList(os.Getenv("GOPATH"))
		if len(gopath) > 0 && gopath[0] != "" {
			gobin = filepath.Join(gopath[0], "bin")
		} else if home := homeDir(); home != "" {
			gobin = filepath.Join(home, "go", "bin")
		}
	}
	if gobin != "" && filepath.Dir(executable) == filepath.Clean(gobin) {
		return "go install"
	}
	return ""
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}

func abbreviateHome(text, home string) string {
	if home == "" || home == string(filepath.Separator) {
		return text
	}
	if text == home {
		return "~"
	}
	return strings.ReplaceAll(text, home+string(filepath.Separator), "~"+string(filepath.Separator))
}

func countNoun(count int, singular, pluralForm string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + pluralForm
}
