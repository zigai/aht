package harness

import (
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Capabilities describes the static capabilities and supported features of an agent harness.
type Capabilities struct {
	Details            registry.DetailCapabilities `json:"details"`
	Harness            registry.Harness            `json:"harness"`
	SessionStart       bool                        `json:"session_start"`
	SessionEnd         bool                        `json:"session_end"`
	RunningIdle        bool                        `json:"running_idle"`
	WaitingPermission  bool                        `json:"waiting_permission"`
	ProcessIdentity    bool                        `json:"process_identity"`
	NativeCatalog      bool                        `json:"native_catalog"`
	Installable        bool                        `json:"installable"`
	Resumable          bool                        `json:"resumable"`
	TitleLookup        bool                        `json:"title_lookup"`
	ScreenSupport      bool                        `json:"screen_support"`
	ScreenFallback     bool                        `json:"screen_fallback"`
	Authority          string                      `json:"authority"`
	IntegrationSource  string                      `json:"integration_source,omitempty"`
	IntegrationVersion int                         `json:"integration_version,omitempty"`
}

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
			Installable:        false,
			Resumable:          false,
			TitleLookup:        false,
			ScreenSupport:      false,
			ScreenFallback:     false,
			Authority:          "",
			IntegrationSource:  "",
			IntegrationVersion: 0,
			Details: registry.DetailCapabilities{
				Native: registry.DetailSupport{
					Permission: false,
					Question:   false,
					UsageLimit: false,
				},
				Screen: registry.DetailSupport{
					Permission: false,
					Question:   false,
					UsageLimit: false,
				},
			},
		}, false
	}

	definition := adapter.Definition()
	_, isInstallable := adapter.(harness.Installable)
	_, isResumable := adapter.(harness.Resumable)
	_, hasTitleLookup := adapter.(harness.TitleReader)
	_, hasNativeCatalog := adapter.(harness.CatalogProvider)
	screenSupport := catalog.SupportsScreen(harnessID)
	policy := (catalog.Rules{}).Policy(harnessID)

	return Capabilities{
		Details:            catalog.DetailCapabilitiesFor(harnessID),
		Harness:            definition.ID,
		SessionStart:       definition.Capabilities.SessionStart,
		SessionEnd:         definition.Capabilities.SessionEnd,
		RunningIdle:        definition.Capabilities.RunningIdle,
		WaitingPermission:  definition.Capabilities.WaitingPermission,
		ProcessIdentity:    definition.Capabilities.ProcessIdentity,
		NativeCatalog:      hasNativeCatalog,
		Installable:        isInstallable,
		Resumable:          isResumable,
		TitleLookup:        hasTitleLookup,
		ScreenSupport:      screenSupport,
		ScreenFallback:     policy.ScreenFallback,
		Authority:          string(policy.Authority),
		IntegrationSource:  policy.Reporter,
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
