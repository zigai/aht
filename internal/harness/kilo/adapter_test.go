package kilo

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("KILO_CONFIG_DIR", "/tmp/kilo-config")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/ignored-xdg")

	if got := kiloPaths(harness.HomeDir()).config; got != "/tmp/kilo-config" {
		t.Fatalf("expected KILO_CONFIG_DIR to win, got %q", got)
	}
}

func TestHistoryOmitsInMemoryDatabase(t *testing.T) {
	home := testharness.Isolate(t, New())
	t.Setenv("KILO_DB", ":memory:")

	sources, err := New().Transcript().Sources(home)
	if err != nil || len(sources) != 0 {
		t.Fatalf("in-memory history sources = %v, %v", sources, err)
	}
}

func TestHistorySourcesFollowDatabaseOverride(t *testing.T) {
	for _, test := range []struct {
		name, database, want string
	}{
		{name: "default directory", want: "kilo"},
		{name: "relative export", database: "selected.db", want: "kilo/selected.db"},
		{name: "absolute export", database: "/var/selected.db", want: "/var/selected.db"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := testharness.Isolate(t, New())
			data := t.TempDir()
			t.Setenv("XDG_DATA_HOME", data)
			t.Setenv("KILO_DB", test.database)

			want := test.want
			if !filepath.IsAbs(want) {
				want = filepath.Join(data, filepath.FromSlash(want))
			}

			sources, err := New().Transcript().Sources(home)
			if err != nil || !slices.Equal(sources, []string{want}) {
				t.Fatalf("history sources = %v, %v; want %s", sources, err, want)
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
		{harness: registry.Harness("kilo"), variable: "KILO_CONFIG_DIR"},
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

func TestSessionTitlesIgnoreWorkingDirectoryWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		plant   func(t *testing.T, workDir string)
	}{
		{registry.Harness("kilo"), func(t *testing.T, workDir string) {
			t.Helper()
			t.Setenv("PATH", "")
			testharness.PlantDatabase(
				t,
				workDir,
				"CREATE TABLE session (id TEXT, title TEXT)",
				"INSERT INTO session VALUES ('abc', 'Decoy')",
				".local",
				"share",
				"kilo",
				"kilo.db",
			)
		}},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			workDir := testharness.WithoutHome(t, New())
			test.plant(t, workDir)

			reader := New()

			titles, err := reader.SessionTitles(
				t.Context(),
				[]registry.ObservationIdentity{{SessionID: "abc"}},
			)
			if err != nil || len(titles) != 1 || titles[0] != "" {
				t.Fatalf("SessionTitles without a home = %q, %v, want one empty title", titles, err)
			}
		})
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
		{"kilo", "permission.asked", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
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
