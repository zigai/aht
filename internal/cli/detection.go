package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/manage"
	"github.com/zigai/aht/pkg/registry"
)

var errTmuxPaneNotLive = manage.ErrPaneNotLive

type explainResult = manage.Explanation

func (app *application) resolvePaneSession(ctx context.Context, paneID, serverID, multiplexerKind string) (registry.Session, error) {
	session, err := app.registryStore().Resolve(ctx, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   registry.MultiplexerKind(multiplexerKind),
		MultiplexerServer: serverID,
		MultiplexerPane:   paneID,
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		return registry.Session{}, fmt.Errorf("resolve pane %q: %w", paneID, err)
	}
	return session, nil
}

func evaluateExplanation(ctx context.Context, session registry.Session, options infoOptions) (manage.Explanation, error) {
	exp, err := manage.ExplainSession(ctx, session, manage.ExplainOptions{
		LiveScreen: !options.disableScreenInspection,
		ConfigDir:  options.configDir,
	})
	if err != nil {
		return exp, fmt.Errorf("explain session: %w", err)
	}
	return exp, nil
}

func (app *application) writeExplanationDetails(result explainResult) error {
	return app.writeHumanDetails([]humanDetail{
		{label: "Registry activity", value: activityString(result.RegistryActivity)},
		{label: "Effective activity", value: result.FinalActivity},
		{label: "Authority", value: result.SelectedAuthority},
		{label: "Process match", value: result.ProcessMatch},
		{label: "Fallback", value: result.FallbackReason},
		{label: "Hook event", value: result.Hook.Event},
		{label: "Hook integration", value: result.Hook.Integration},
		{label: "Hook age", value: result.Hook.Age},
		{label: "Hook fresh", value: strconv.FormatBool(result.Hook.Fresh)},
		{label: "Hook reason", value: result.Hook.FreshnessReason},
		{label: "Screen evaluated", value: strconv.FormatBool(result.Screen.Evaluated)},
		{label: "Screen unavailable", value: result.Screen.UnavailableReason},
		{label: "Screen activity", value: string(result.Screen.Decision.Activity)},
		{label: "Screen reason", value: result.Screen.Decision.Reason},
		{label: "Screen rule", value: result.Screen.Decision.RuleID},
		{label: "Manifest", value: result.Screen.Decision.ManifestSource},
		{label: "Manifest version", value: strconv.Itoa(result.Screen.Decision.ManifestVersion)},
		{label: "Screen warning", value: result.Screen.Decision.Warning},
		{label: "Screen error", value: result.Screen.Error},
	})
}

func activityString(activity *registry.Activity) string {
	if activity == nil {
		return "none"
	}
	return string(*activity)
}
