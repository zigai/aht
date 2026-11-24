package pi_test

import (
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/harness/pi"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestStockDefaultScreenManifest(t *testing.T) {
	t.Parallel()

	manifest, err := agentstate.ParseManifest(
		[]byte(pi.New().Definition().ScreenManifest),
		registry.Harness("pi"),
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		screen string
		want   registry.Activity
		rule   string
	}{
		{
			name:   "stock idle footer non-reasoning model",
			screen: "~/project\n0.0%/32k (auto)   (fake) fake",
			want:   registry.ActivityIdle,
			rule:   "default_input_prompt",
		},
		{
			name: "stock idle footer with token stats and cost",
			screen: "~/project (main)\n" +
				"↑1.2k ↓340 R500 W100 CH80.0% $0.012 " +
				"12.5%/200k (auto)   (anthropic) claude-sonnet-4 • high",
			want: registry.ActivityIdle,
			rule: "default_input_prompt",
		},
		{
			name:   "stock idle footer post-compaction unknown percent",
			screen: "~/project\n?/1.0M (auto)   gemini-2.5-pro",
			want:   registry.ActivityIdle,
			rule:   "default_input_prompt",
		},
		{
			name:   "stock idle footer without auto-compact",
			screen: "~/project\n4.2%/128k   model-a",
			want:   registry.ActivityIdle,
			rule:   "default_input_prompt",
		},
		{
			name:   "stock working overrides stock footer",
			screen: "Working (esc to interrupt)\n~/project\n0.0%/32k (auto)   (fake) fake",
			want:   registry.ActivityRunning,
			rule:   "working_interruptible",
		},
		{
			name:   "custom working overrides stock footer",
			screen: "  ⠋ Working... (12s)\n~/project\n0.0%/32k (auto)   (fake) fake",
			want:   registry.ActivityRunning,
			rule:   "custom_working",
		},
		{
			name:   "stock footer outside bottom region stays unknown",
			screen: "~/project\n0.0%/32k (auto)   (fake) fake\n" + strings.Repeat("output\n", 12),
			want:   registry.ActivityUnknown,
			rule:   "",
		},
		{
			name:   "custom footer keeps custom_input_prompt rule",
			screen: " ~/src · Codex · Model-A · max 18.3%/272k",
			want:   registry.ActivityIdle,
			rule:   "custom_input_prompt",
		},
		{
			name:   "ordinary shell output stays unknown",
			screen: "ordinary shell output",
			want:   registry.ActivityUnknown,
			rule:   "",
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
