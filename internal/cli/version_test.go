package cli

import (
	"runtime/debug"
	"testing"
)

func TestFormatVersion(t *testing.T) {
	t.Parallel()

	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "123456789abcdef"},
			{Key: "vcs.modified", Value: "true"},
		},
	}

	tests := []struct {
		name    string
		version string
		commit  string
		dirty   string
		info    *debug.BuildInfo
		want    string
	}{
		{"defaults", "dev", "none", "", nil, "aht dev (none)"},
		{"embedded metadata", "dev", "none", "", info, "aht v1.2.3 (1234567, dirty)"},
		{"release overrides", "v2.0.0", "abcdef0", "false", info, "aht v2.0.0 (abcdef0)"},
		{
			"local overrides",
			"v2.0.0-dirty",
			"abcdef0",
			"",
			info,
			"aht v2.0.0-dirty (abcdef0, dirty)",
		},
		{"explicit dirty", "v2.0.0", "abcdef0", "true", nil, "aht v2.0.0 (abcdef0, dirty)"},
		{
			"development module",
			"dev",
			"none",
			"",
			&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			"aht dev (none)",
		},
		{
			"short revision", "dev", "none", "",
			&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}},
			"aht dev (abc)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := formatVersion(tt.version, tt.commit, tt.dirty, tt.info); got != tt.want {
				t.Fatalf("formatVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
