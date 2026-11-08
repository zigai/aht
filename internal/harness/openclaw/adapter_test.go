package openclaw

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestHistoryFollowsNativeStateDirectory(t *testing.T) {
	for _, profile := range []string{"", "default", "work"} {
		t.Run(profile, func(t *testing.T) {
			home := testharness.Isolate(t, New())
			root := t.TempDir()
			t.Setenv("OPENCLAW_HOME", root)
			t.Setenv("OPENCLAW_PROFILE", profile)

			state := ".openclaw"
			if profile == "work" {
				state = ".openclaw-work"
			}

			sources, err := New().Transcript().Sources(home)
			if err != nil ||
				!slices.Equal(sources, []string{filepath.Join(root, state, "agents")}) {
				t.Fatalf("history sources = %v, %v", sources, err)
			}
		})
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("openclaw"), variable: "OPENCLAW_STATE_DIR"},
		{
			harness: registry.Harness(
				"openclaw",
			),
			variable: "OPENCLAW_CONFIG_PATH",
			value: func(dir string) string {
				return filepath.Join(
					dir,
					"custom.json",
				)
			},
		},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, New())

				if !knownHome {
					t.Setenv("HOME", "")
					t.Setenv("USERPROFILE", "")
				}

				override := t.TempDir()

				value := override
				if test.value != nil {
					value = test.value(override)
				}

				t.Setenv(test.variable, value)

				locations := New().Locations("")

				config := testharness.LocationPaths(locations, harness.LocationKindConfig)
				if len(config) == 0 || slices.ContainsFunc(config, func(path string) bool {
					return !strings.HasPrefix(path, override+string(filepath.Separator)) &&
						!strings.HasPrefix(path, "/etc/")
				}) {
					t.Errorf("config locations with %s=%s = %v", test.variable, value, config)
				}
			})
		}
	}
}
