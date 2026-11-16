//go:build compatibility && harness

package hostcompat

import (
	"os"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestCurrentHarnessLifecycle(t *testing.T) {
	harnessID := registry.Harness(strings.TrimSpace(os.Getenv("AHT_COMPAT_HARNESS")))
	if harnessID == "" {
		t.Fatal("AHT_COMPAT_HARNESS is required; use `just compatibility <harness>`")
	}

	contract, err := contractFor(harnessID)
	if err != nil {
		t.Fatal(err)
	}

	oracle := testharness.Build(t)
	host := newIsolatedHost(t, contract, oracle, true)
	host.assertIntegrationCurrent(t)
	host.assertVersion(t)
	host.assertHostLoadsIntegration(t)

	if contract.Level == compatibilityLevelDiscovery {
		t.Skipf(
			"%s discovery-only: installation, current integration, and "+
				"installed version checked; no lifecycle, interruption, "+
				"permission, or resume claim. Scope: %s",
			contract.ID,
			contract.Scope,
		)

		return
	}

	t.Run("successful_native_terminal", host.runLifecycle)
	t.Run("active_interruption", func(t *testing.T) {
		interrupted := newIsolatedHost(t, contract, oracle, true)
		interrupted.interrupt = true
		interrupted.runLifecycle(t)
	})
	runPermissionScenarios(t, contract, oracle)
}
