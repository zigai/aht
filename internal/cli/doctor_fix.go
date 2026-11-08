package cli

import (
	"os"
	"strings"

	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (app *application) doctorFixCommand(fix *manage.DoctorFix) string {
	switch fix.Action {
	case manage.DoctorActionRefreshHistory:
		return "aht manage history refresh"
	case manage.DoctorActionResetStore:
		prefix := "aht"
		if fix.Target != registry.DefaultStorePath() {
			prefix += " --store " + doctorFixPath(fix.Target)
		}

		return prefix + " manage state reset --force"
	case manage.DoctorActionEnableTracker:
		return app.doctorTrackerCommand("enable")
	case manage.DoctorActionInspectTracker:
		return app.doctorTrackerCommand("status")
	case manage.DoctorActionRemoveHealth:
		return "rm " + doctorFixPath(fix.Target)
	case manage.DoctorActionReplaceIntegration:
		return "aht manage integrations install " + harness.ShellQuote(fix.Target) + " --force"
	case manage.DoctorActionUpgrade:
		return "aht manage upgrade"
	case "":
		return ""
	default:
		panic("unknown doctor action: " + fix.Action)
	}
}

func (app *application) doctorTrackerCommand(action string) string {
	command := "aht --store " + doctorFixPath(app.resolvedStorePath())

	path := app.doctorConfigPath()
	if app.noConfig {
		command += " --no-config"
	} else if path != "" {
		command += " --config " + doctorFixPath(path)
	}

	command += " manage tracker " + action
	if !app.noConfig && path == "-" {
		command += " < saved-config.toml"
	}

	return command
}

func (app *application) doctorConfigPath() string {
	if app.configPath != "" {
		return app.configPath
	}

	return strings.TrimSpace(os.Getenv(config.ConfigEnv))
}

func doctorFixPath(path string) string {
	path = formatHumanPath(path)
	if path == "~" {
		return path
	}

	if suffix, found := strings.CutPrefix(path, "~/"); found {
		return "~/" + harness.ShellQuote(suffix)
	}

	return harness.ShellQuote(path)
}

func (app *application) doctorFixManual(fix *manage.DoctorFix) string {
	command := app.doctorFixCommand(fix)

	manual := fix.Manual
	if !app.noConfig && app.doctorConfigPath() == "-" &&
		(fix.Action == manage.DoctorActionEnableTracker || fix.Action == manage.DoctorActionInspectTracker) {
		if manual != "" {
			manual += ". "
		}

		manual += "Save the same TOML configuration supplied to doctor as saved-config.toml, then run"
	}

	if manual == "" || command == "" || fix.Action == manage.DoctorActionUpgrade {
		return manual
	}

	return strings.TrimSuffix(manual, ":") + ": " + command
}
