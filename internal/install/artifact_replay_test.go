//go:build integration

package install

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	harnesscatalog "github.com/zigai/aht/v2/internal/harness/catalog"
)

func TestIntegrationManagedArtifactsUseV2Reports(t *testing.T) {
	t.Parallel()
	for _, adapter := range harnesscatalog.All() {
		definition := adapter.Definition()
		installable, ok := adapter.(harness.Installable)
		if !ok {
			continue
		}
		plan := installable.InstallPlan("/tmp/aht")
		if len(plan.Actions) == 0 {
			t.Fatalf("%s has no integration actions", definition.ID)
		}
		for _, action := range plan.Actions {
			content := fmt.Sprintf("%#v", action)
			if strings.Contains(content, "--state") || strings.Contains(content, "--source") || strings.Contains(content, "--confidence") {
				t.Fatalf("%s generated a v1 report flag: %q", definition.ID, content)
			}
			if strings.Contains(content, "aht") && !strings.Contains(content, "--activity") && !strings.Contains(content, "--event") {
				t.Fatalf("%s generated hook without v2 activity/event dimension: %q", definition.ID, content)
			}
		}
	}
}
