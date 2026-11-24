package omp_test

import (
	"testing"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/harness/omp"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestScreenManifestStockDefaultUI(t *testing.T) {
	t.Parallel()

	manifest, err := agentstate.ParseManifest(
		[]byte(omp.New().Definition().ScreenManifest),
		registry.Harness("omp"),
	)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	tests := []struct {
		name   string
		screen string
		want   registry.Activity
		rule   string
	}{
		{
			name:   "stock working loader with unicode ellipsis",
			screen: "  ⎋ Working…\nπ > ⬢ AHT fakeprovider > 📁 ~/repo",
			want:   registry.ActivityRunning,
			rule:   "working_status",
		},
		{
			name:   "stock powerline spinner working",
			screen: "▶─────────╎\n⠧ 1s > ⬢ AHT fakeprovider > 📁 ~/repo",
			want:   registry.ActivityRunning,
			rule:   "working_status",
		},
		{
			name:   "stock working suppresses historical error",
			screen: "API Error: old failure\n  ⎋ Working…\n⠧ 2s > ⬢ AHT fakeprovider > 📁 ~/repo",
			want:   registry.ActivityRunning,
			rule:   "working_status",
		},
		{
			name:   "stock idle powerline footer",
			screen: "π > ⬢ AHT fakeprovider > 📁 ~/repo",
			want:   registry.ActivityIdle,
			rule:   "powerline_input_prompt",
		},
		{
			name:   "stock idle editor chrome",
			screen: "▶─────────╎ 12%",
			want:   registry.ActivityIdle,
			rule:   "powerline_input_prompt",
		},
		{
			name:   "historical bare Working ellipsis stays idle",
			screen: "Working…\nπ > ⬢ AHT fakeprovider > 📁 ~/repo",
			want:   registry.ActivityIdle,
			rule:   "powerline_input_prompt",
		},
		{
			name:   "legacy custom working still matches custom_working",
			screen: "  ⠋ Working... (40s)",
			want:   registry.ActivityRunning,
			rule:   "custom_working",
		},
		{
			name:   "legacy custom footer still matches custom_input_prompt",
			screen: " ~/src/app · Codex · Model-A · medium 7.1%/1M",
			want:   registry.ActivityIdle,
			rule:   "custom_input_prompt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decision := manifest.Evaluate(agentstate.NormalizeSnapshot(test.screen, ""))
			if decision.Activity != test.want || decision.RuleID != test.rule {
				t.Fatalf(
					"decision = %#v, want activity %q rule %q",
					decision,
					test.want,
					test.rule,
				)
			}
		})
	}
}
