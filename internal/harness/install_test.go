package harness_test

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestInstallPlanNativeCatalog(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		actions []harness.InstallAction
		want    bool
	}{
		{name: "empty", actions: nil, want: false},
		{
			name:    "native catalog",
			actions: []harness.InstallAction{harness.NativeCatalogAction{Label: "sessions"}},
			want:    true,
		},
		{
			name: "multiple catalogs",
			actions: []harness.InstallAction{
				harness.NativeCatalogAction{Label: "sessions"},
				harness.NativeCatalogAction{Label: "threads"},
			},
			want: true,
		},
		{name: "managed action", actions: []harness.InstallAction{harness.ShimAction{}}, want: false},
		{
			name:    "catalog then managed action",
			actions: []harness.InstallAction{harness.NativeCatalogAction{Label: "sessions"}, harness.ShimAction{}},
			want:    false,
		},
		{
			name:    "managed action then catalog",
			actions: []harness.InstallAction{harness.ShimAction{}, harness.NativeCatalogAction{Label: "sessions"}},
			want:    false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := harness.InstallPlan{Actions: test.actions}

			catalog, got := plan.NativeCatalog()
			if got != test.want {
				t.Fatalf("NativeCatalog() supported = %t, want %t", got, test.want)
			}

			if got && catalog.Label != "sessions" {
				t.Fatalf("NativeCatalog() label = %q, want sessions", catalog.Label)
			}
		})
	}
}

func TestRenderScriptTemplateExpandsQueueAndLeavesNoPlaceholders(t *testing.T) {
	t.Parallel()

	sampleTemplate := "{{MANAGED_MARKER}}\nconst id = {{INTEGRATION_ID}};\nconst v = " +
		"{{INTEGRATION_VERSION}};\nconst bin = {{BINARY}};\nconst src " +
		"= {{SOURCE}};\n{{TYPESCRIPT_QUEUE}}\nconst format = " +
		"{nested:{}};\n"
	rendered := harness.RenderScriptTemplate(
		sampleTemplate,
		"/bin/test-aht",
		harness.Definition{
			ID:                 "test-integration",
			IntegrationVersion: 42,
			IntegrationSource:  "test-source",
		},
	)

	placeholderPattern := regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)
	if match := placeholderPattern.FindString(rendered); match != "" {
		t.Fatalf("rendered template contains unresolved placeholder %q:\n%s", match, rendered)
	}

	for _, expected := range []string{
		harness.ManagedMarker,
		`const id = test-integration;`,
		`const v = 42;`,
		`const bin = "/bin/test-aht";`,
		`const src = "test-source";`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered template missing expected fragment %q", expected)
		}
	}
}

func TestShellQuoteRecoversArguments(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(property *rapid.T) {
		words := rapid.SliceOf(rapid.StringOf(rapid.Rune().Filter(func(character rune) bool {
			return character != 0
		}))).Draw(property, "arguments")
		quoted := make([]string, len(words))

		var expected []byte

		for index, word := range words {
			quoted[index] = harness.ShellQuote(word)
			expected = append(expected, word...)
			expected = append(expected, 0)
		}

		script := "set -- " + strings.Join(
			quoted,
			" ",
		) + "; for argument do printf '%s\\000' \"$argument\"; done"

		output, err := exec.CommandContext(property.Context(), "sh", "-c", script).Output()
		if err != nil {
			property.Fatalf("shell rejected arguments %q: %v", words, err)
		}

		if !bytes.Equal(output, expected) {
			property.Fatalf("shell arguments = %q, want %q", output, expected)
		}
	})
}

func TestReportHookCommandRendersTypedTransitionDimension(t *testing.T) {
	t.Parallel()

	activity := harness.ReportHookCommand(
		"aht",
		harness.Definition{ID: "sample", IntegrationSource: "sample-hook", IntegrationVersion: 1},
		harness.ReportStdinRaw,
		registry.ActivityRunning,
		"turn",
	)
	if !strings.Contains(activity, "--activity running") ||
		strings.Contains(activity, "--presence") {
		t.Fatalf("activity command = %q", activity)
	}

	presence := harness.ReportHookCommand(
		"aht",
		harness.Definition{ID: "sample", IntegrationSource: "sample-hook", IntegrationVersion: 1},
		harness.ReportStdinRaw,
		registry.PresenceGone,
		"stop",
	)
	if !strings.Contains(presence, "--presence gone") || strings.Contains(presence, "--activity") {
		t.Fatalf("presence command = %q", presence)
	}
}

func TestReportHookCommandRejectsInvalidTypedTransition(t *testing.T) {
	t.Parallel()

	deferredPanic := false

	func() {
		defer func() {
			deferredPanic = recover() != nil
		}()

		_ = harness.ReportHookCommand(
			"aht",
			harness.Definition{
				ID:                 "sample",
				IntegrationSource:  "sample-hook",
				IntegrationVersion: 1,
			},
			harness.ReportStdinRaw,
			registry.Activity("bogus"),
			"turn",
		)
	}()

	if !deferredPanic {
		t.Fatal("ReportHookCommand accepted an invalid activity")
	}
}
