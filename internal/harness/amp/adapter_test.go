package amp

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestConfigDirXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/custom-xdg")

	if got := ampConfigDir(); got != "/tmp/custom-xdg/amp" {
		t.Fatalf("expected XDG_CONFIG_HOME/amp, got %q", got)
	}
}

func TestNativeDetailsUseExplicitPromptSignals(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		harness    registry.Harness
		event      string
		activity   registry.Activity
		attributes map[string]string
		want       *registry.ActivityDetail
	}{
		{
			"amp",
			"thread.state",
			registry.ActivityWaiting,
			map[string]string{
				"amp_thread_state": "awaiting-approval",
			},
			new(
				registry.ActivityDetailPermission,
			),
		},
	} {
		t.Run(string(test.harness)+"/"+test.event+string(test.activity), func(t *testing.T) {
			report := &registry.Report{
				Event:      test.event,
				Activity:   &test.activity,
				Attributes: test.attributes,
			}

			var detail *registry.ActivityDetail
			if reader, ok := any(New()).(harness.ActivityDetailer); ok {
				detail = reader.ActivityDetail(report.Event, *report.Activity, report.Attributes)
			}

			if !reflect.DeepEqual(detail, test.want) {
				t.Fatalf("detail = %v, want %v", detail, test.want)
			}
		})
	}
}
