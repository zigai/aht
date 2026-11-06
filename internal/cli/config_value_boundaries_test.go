package cli

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/internal/config"
)

func TestConfigValuePreservesDefaultsTypesAndNormalizedKeys(t *testing.T) {
	t.Parallel()

	cfg := config.Config{}

	for _, test := range []struct {
		key  string
		want any
	}{
		{"ui.default_presence", ""},
		{"ui.sort", ""},
		{"ui.sort_desc", false},
		{"ui.absolute_time", false},
		{"ui.time_format", ""},
		{"retention.tombstone_ttl", ""},
		{"filter.ignore_harnesses", []string(nil)},
		{"filter.ignore_paths", []string(nil)},
		{"tracker.interval", ""},
		{"tracker.grace_period", ""},
		{"tracker.quiet", false},
		{"detection.manifests_dir", ""},
		{"detection.screen_inspection", true},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Parallel()

			got, ok := getConfigValue(cfg, test.key)
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("value = %#v (%T), %v; want %#v (%T)", got, got, ok, test.want, test.want)
			}
		})
	}

	if value, ok := getConfigValue(cfg, " UI.SORT_DESC \t"); !ok || value != false {
		t.Fatalf("normalized value = %#v, %v", value, ok)
	}

	if value, ok := getConfigValue(cfg, "unknown"); ok || value != nil {
		t.Fatalf("unknown key = %#v, %v", value, ok)
	}
}

func TestConfigValuePreservesExplicitBooleanOverrides(t *testing.T) {
	t.Parallel()

	for _, value := range []bool{false, true} {
		cfg := config.Config{}
		cfg.UI.SortDesc = &value
		cfg.UI.AbsoluteTime = &value
		cfg.Tracker.Quiet = &value
		cfg.Detection.ScreenInspection = &value

		for _, key := range []string{
			"ui.sort_desc", "ui.absolute_time", "tracker.quiet", "detection.screen_inspection",
		} {
			if got, ok := getConfigValue(cfg, key); !ok || got != value {
				t.Fatalf("%s = %#v, %v; want %v", key, got, ok, value)
			}
		}
	}
}
