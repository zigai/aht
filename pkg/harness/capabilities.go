package harness

import (
	"context"
	"fmt"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/pkg/registry"
)

type (
	// Capabilities describes the static capabilities and supported features of an agent harness.
	Capabilities struct {
		Harness            registry.Harness `json:"harness"`
		SessionStart       bool             `json:"session_start"`
		SessionEnd         bool             `json:"session_end"`
		RunningIdle        bool             `json:"running_idle"`
		WaitingPermission  bool             `json:"waiting_permission"`
		ProcessIdentity    bool             `json:"process_identity"`
		NativeCatalog      bool             `json:"native_catalog"`
		TTYTmuxContext     bool             `json:"tty_tmux_context"`
		Installable        bool             `json:"installable"`
		Resumable          bool             `json:"resumable"`
		TitleLookup        bool             `json:"title_lookup"`
		ScreenSupport      bool             `json:"screen_support"`
		ScreenFallback     bool             `json:"screen_fallback"`
		Authority          string           `json:"authority"`
		IntegrationSource  string           `json:"integration_source,omitempty"`
		IntegrationVersion int              `json:"integration_version,omitempty"`
	}

	// RuntimeStatus describes the installed/runtime state of a harness integration on the local system.
	RuntimeStatus struct {
		Harness   registry.Harness `json:"harness"`
		Installed bool             `json:"installed"`
		Current   bool             `json:"current"`
		Status    string           `json:"status"`
		Message   string           `json:"message,omitempty"`
	}
)

// CapabilitiesFor returns the static capabilities of harnessID.
// If harnessID is unrecognized, it returns an empty Capabilities and false.
func CapabilitiesFor(harnessID registry.Harness) (Capabilities, bool) {
	adapter, ok := catalog.Find(harnessID)
	if !ok {
		return Capabilities{
			Harness:            "",
			SessionStart:       false,
			SessionEnd:         false,
			RunningIdle:        false,
			WaitingPermission:  false,
			ProcessIdentity:    false,
			NativeCatalog:      false,
			TTYTmuxContext:     false,
			Installable:        false,
			Resumable:          false,
			TitleLookup:        false,
			ScreenSupport:      false,
			ScreenFallback:     false,
			Authority:          "",
			IntegrationSource:  "",
			IntegrationVersion: 0,
		}, false
	}
	definition := adapter.Definition()
	_, isInstallable := adapter.(harness.Installable)
	_, isResumable := adapter.(harness.Resumable)
	_, hasTitleLookup := adapter.(harness.TitleReader)
	screenSupport := catalog.SupportsScreen(harnessID)
	authority, fallback, source := catalog.PolicyFor(harnessID)

	return Capabilities{
		Harness:            definition.ID,
		SessionStart:       definition.Capabilities.SessionStart,
		SessionEnd:         definition.Capabilities.SessionEnd,
		RunningIdle:        definition.Capabilities.RunningIdle,
		WaitingPermission:  definition.Capabilities.WaitingPermission,
		ProcessIdentity:    definition.Capabilities.ProcessIdentity,
		NativeCatalog:      definition.Capabilities.NativeCatalog,
		TTYTmuxContext:     definition.Capabilities.TTYTmuxContext,
		Installable:        isInstallable,
		Resumable:          isResumable,
		TitleLookup:        hasTitleLookup,
		ScreenSupport:      screenSupport,
		ScreenFallback:     fallback,
		Authority:          string(authority),
		IntegrationSource:  source,
		IntegrationVersion: definition.IntegrationVersion,
	}, true
}

// AllCapabilities returns the static capabilities of all supported harnesses in canonical order.
func AllCapabilities() []Capabilities {
	adapters := catalog.All()
	result := make([]Capabilities, 0, len(adapters))
	for _, adapter := range adapters {
		def := adapter.Definition()
		caps, ok := CapabilitiesFor(def.ID)
		if ok {
			result = append(result, caps)
		}
	}
	return result
}

// InspectRuntime inspects the runtime installation status of a harness integration.
func InspectRuntime(ctx context.Context, id registry.Harness, binary string) (RuntimeStatus, error) {
	status, err := install.InspectContext(ctx, id, binary)
	if err != nil {
		return RuntimeStatus{
			Harness:   id,
			Installed: false,
			Current:   false,
			Status:    "error",
			Message:   err.Error(),
		}, fmt.Errorf("inspect runtime for harness %s: %w", id, err)
	}

	installed := status.Status == install.ArtifactCurrent || status.Status == install.ArtifactStale
	current := status.Status == install.ArtifactCurrent

	return RuntimeStatus{
		Harness:   id,
		Installed: installed,
		Current:   current,
		Status:    string(status.Status),
		Message:   status.Message,
	}, nil
}

// AllRuntimeStatuses inspects the runtime installation status of all supported harnesses.
func AllRuntimeStatuses(ctx context.Context, binary string) []RuntimeStatus {
	all := Supported()
	results := make([]RuntimeStatus, 0, len(all))
	for _, id := range all {
		st, err := InspectRuntime(ctx, id, binary)
		if err != nil {
			results = append(results, RuntimeStatus{
				Harness:   id,
				Installed: false,
				Current:   false,
				Status:    "error",
				Message:   err.Error(),
			})
			continue
		}
		results = append(results, st)
	}
	return results
}
