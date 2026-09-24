package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
		errSub   string
	}{
		{input: "", expected: 0},
		{input: "0s", expected: 0},
		{input: "10s", expected: 10 * time.Second},
		{input: "5m", expected: 5 * time.Minute},
		{input: "24h", expected: 24 * time.Hour},
		{input: "1d", expected: 24 * time.Hour},
		{input: "7d", expected: 7 * 24 * time.Hour},
		{input: "14D", expected: 14 * 24 * time.Hour},
		{input: "7", wantErr: true, errSub: "missing unit suffix"},
		{input: "invalid", wantErr: true},
		{input: "-1d", expected: -24 * time.Hour},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseDuration(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) expected error, got nil", tc.input)
				}
				if tc.errSub != "" && !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("ParseDuration(%q) error %q does not contain %q", tc.input, err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func isolateConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv(ConfigEnv, "")
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(t.TempDir(), "system"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "user"))
}

func TestLoadMissingDefaultFile(t *testing.T) {
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "does-not-exist.toml")
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(tempDir, "system"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempDir, "user"))
	t.Setenv(ConfigEnv, nonExistentPath)

	cfg, resolved, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") with missing default file returned unexpected error: %v", err)
	}
	if resolved != nonExistentPath {
		t.Fatalf("expected resolved path %q, got %q", nonExistentPath, resolved)
	}
	if cfg.UI.DefaultPresence != "all" || cfg.UI.Sort != "updated" {
		t.Fatalf("expected default config with presence 'all' and sort 'updated', got %+v", cfg)
	}
}

func TestLoadExplicitMissingFile(t *testing.T) {
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "missing.toml")

	_, _, err := Load(nonExistentPath)
	if err == nil {
		t.Fatal("Load with explicit missing file expected error, got nil")
	}
	if !strings.Contains(err.Error(), "config file not found") {
		t.Fatalf("error %q does not contain 'config file not found'", err.Error())
	}
}

//nolint:cyclop,gocognit // test verifies all fields of full configuration
func TestLoadValidFullTOML(t *testing.T) {
	isolateConfigEnv(t)
	content := `
[ui]
default_presence = "live"
sort = "created"
sort_desc = true
absolute_time = true
time_format = "absolute"

[retention]
auto_clean = true
max_gone_age = "7d"

[filter]
ignore_harnesses = ["copilot", "gemini"]
ignore_paths = ["/tmp/*", "**/node_modules/**"]

[tracker]
interval = "5s"
grace_period = "15s"
quiet = true

[detection]
manifests_dir = "/custom/manifests"
screen_inspection = false
`
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, resolved, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load valid config failed: %v", err)
	}
	if resolved != configPath {
		t.Fatalf("resolved path %q != %q", resolved, configPath)
	}

	// UI
	if cfg.UI.DefaultPresence != "live" {
		t.Errorf("UI.DefaultPresence = %q, want 'live'", cfg.UI.DefaultPresence)
	}
	if cfg.UI.Sort != "created" {
		t.Errorf("UI.Sort = %q, want 'created'", cfg.UI.Sort)
	}
	if cfg.UI.SortDesc == nil || !*cfg.UI.SortDesc {
		t.Errorf("UI.SortDesc = %v, want true", cfg.UI.SortDesc)
	}
	if cfg.UI.AbsoluteTime == nil || !*cfg.UI.AbsoluteTime {
		t.Errorf("UI.AbsoluteTime = %v, want true", cfg.UI.AbsoluteTime)
	}
	if cfg.UI.TimeFormat != "absolute" {
		t.Errorf("UI.TimeFormat = %q, want 'absolute'", cfg.UI.TimeFormat)
	}

	// Retention
	if cfg.Retention.AutoClean == nil || !*cfg.Retention.AutoClean {
		t.Errorf("Retention.AutoClean = %v, want true", cfg.Retention.AutoClean)
	}
	if cfg.Retention.MaxGoneAge != "7d" {
		t.Errorf("Retention.MaxGoneAge = %q, want '7d'", cfg.Retention.MaxGoneAge)
	}

	// Filter
	if len(cfg.Filter.IgnoreHarnesses) != 2 || cfg.Filter.IgnoreHarnesses[0] != "copilot" || cfg.Filter.IgnoreHarnesses[1] != "gemini" {
		t.Errorf("Filter.IgnoreHarnesses = %v, want ['copilot', 'gemini']", cfg.Filter.IgnoreHarnesses)
	}
	if len(cfg.Filter.IgnorePaths) != 2 || cfg.Filter.IgnorePaths[0] != "/tmp/*" {
		t.Errorf("Filter.IgnorePaths = %v", cfg.Filter.IgnorePaths)
	}

	// Tracker
	if cfg.Tracker.Interval != "5s" {
		t.Errorf("Tracker.Interval = %q, want '5s'", cfg.Tracker.Interval)
	}
	if cfg.Tracker.GracePeriod != "15s" {
		t.Errorf("Tracker.GracePeriod = %q, want '15s'", cfg.Tracker.GracePeriod)
	}
	if cfg.Tracker.Quiet == nil || !*cfg.Tracker.Quiet {
		t.Errorf("Tracker.Quiet = %v, want true", cfg.Tracker.Quiet)
	}

	// Detection
	if cfg.Detection.ManifestsDir != "/custom/manifests" {
		t.Errorf("Detection.ManifestsDir = %q, want '/custom/manifests'", cfg.Detection.ManifestsDir)
	}
	if cfg.Detection.ScreenInspection == nil || *cfg.Detection.ScreenInspection {
		t.Errorf("Detection.ScreenInspection = %v, want false", cfg.Detection.ScreenInspection)
	}
}

func TestLoadUnknownField(t *testing.T) {
	isolateConfigEnv(t)
	content := `
[ui]
unknown_setting = "invalid"
`
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Load(configPath)
	if err == nil {
		t.Fatal("Load with unknown field expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unused") && !strings.Contains(err.Error(), "unknown_setting") {
		t.Fatalf("expected unused/unknown field error, got: %v", err)
	}
}

func TestLoadSyntaxError(t *testing.T) {
	isolateConfigEnv(t)
	content := `
[ui
broken toml syntax
`
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Load(configPath)
	if err == nil {
		t.Fatal("Load with syntax error expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse config file") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	isolateConfigEnv(t)
	tests := []struct {
		name   string
		toml   string
		errSub string
	}{
		{
			name: "invalid presence",
			toml: `[ui]
default_presence = "bogus"`,
			errSub: "invalid ui.default_presence",
		},
		{
			name: "invalid sort",
			toml: `[ui]
sort = "nonexistent"`,
			errSub: "invalid ui.sort",
		},
		{
			name: "invalid time format",
			toml: `[ui]
time_format = "rfc3339"`,
			errSub: "invalid ui.time_format",
		},
		{
			name: "negative max gone age",
			toml: `[retention]
max_gone_age = "-5s"`,
			errSub: "retention.max_gone_age must be non-negative",
		},
		{
			name: "invalid max gone age unit",
			toml: `[retention]
max_gone_age = "10"`,
			errSub: "missing unit suffix",
		},
		{
			name: "negative tracker interval",
			toml: `[tracker]
interval = "-1s"`,
			errSub: "tracker.interval must be positive",
		},
		{
			name: "zero tracker interval",
			toml: `[tracker]
interval = "0s"`,
			errSub: "tracker.interval must be positive",
		},
		{
			name: "negative grace period",
			toml: `[tracker]
grace_period = "-10s"`,
			errSub: "tracker.grace_period must be non-negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.toml")
			if err := os.WriteFile(configPath, []byte(tc.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load(configPath)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.errSub)
			}
		})
	}
}

func TestSettingsEnvironmentLayer(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, "user", "aht")
	t.Setenv(ConfigEnv, "")
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(dir, "system"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "user"))
	t.Setenv("AHT_UI_SORT", "created")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "config.toml"), []byte("[ui]\ndefault_presence = 'gone'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, _, err := LoadWithMetadata(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Sort != "created" || cfg.UI.DefaultPresence != "gone" {
		t.Fatalf("layered config = %+v", cfg.UI)
	}
	if origin, ok := meta.Where("ui.sort"); !ok || origin.Source != strata.SourceEnv || origin.Path != "AHT_UI_SORT" {
		t.Fatalf("environment origin = %+v, found %t", origin, ok)
	}
	cfg, _, err = LoadWithOptions(Options{NoConfig: true})
	if err != nil || cfg.UI.Sort != "created" || cfg.UI.DefaultPresence != "all" {
		t.Fatalf("no-config result = %+v, error = %v", cfg, err)
	}
}

func TestMaxFileSizeLimit(t *testing.T) {
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	largeConfig := filepath.Join(tempDir, "large.toml")

	// Create a file > 1 MiB
	f, err := os.Create(largeConfig)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024*1024+10)
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = Load(largeConfig)
	if err == nil {
		t.Fatal("Load large file expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds 1 MiB limit") {
		t.Fatalf("expected 1 MiB limit error, got: %v", err)
	}
}

//nolint:cyclop,gocognit // test verifies all fields of default configuration template
func TestDefaultConfigTemplateValid(t *testing.T) {
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "default_template.toml")

	tmpl := DefaultConfigTemplate()
	if err := os.WriteFile(configPath, []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, resolved, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load(defaultTemplate) failed: %v", err)
	}
	if resolved != configPath {
		t.Fatalf("expected resolved path %q, got %q", configPath, resolved)
	}

	if cfg.UI.DefaultPresence != "all" {
		t.Errorf("expected UI.DefaultPresence='all', got %q", cfg.UI.DefaultPresence)
	}
	if cfg.UI.Sort != "updated" {
		t.Errorf("expected UI.Sort='updated', got %q", cfg.UI.Sort)
	}
	if cfg.UI.SortDesc == nil || *cfg.UI.SortDesc {
		t.Errorf("expected UI.SortDesc=false, got %v", cfg.UI.SortDesc)
	}
	if cfg.UI.AbsoluteTime == nil || *cfg.UI.AbsoluteTime {
		t.Errorf("expected UI.AbsoluteTime=false, got %v", cfg.UI.AbsoluteTime)
	}
	if cfg.UI.TimeFormat != "relative" {
		t.Errorf("expected UI.TimeFormat='relative', got %q", cfg.UI.TimeFormat)
	}
	if cfg.Retention.AutoClean == nil || *cfg.Retention.AutoClean {
		t.Errorf("expected Retention.AutoClean=false, got %v", cfg.Retention.AutoClean)
	}
	if cfg.Retention.MaxGoneAge != "7d" {
		t.Errorf("expected Retention.MaxGoneAge='7d', got %q", cfg.Retention.MaxGoneAge)
	}
	if len(cfg.Filter.IgnoreHarnesses) != 0 {
		t.Errorf("expected empty IgnoreHarnesses, got %v", cfg.Filter.IgnoreHarnesses)
	}
	if len(cfg.Filter.IgnorePaths) != 0 {
		t.Errorf("expected empty IgnorePaths, got %v", cfg.Filter.IgnorePaths)
	}
	if cfg.Tracker.Interval != "300ms" {
		t.Errorf("expected Tracker.Interval='300ms', got %q", cfg.Tracker.Interval)
	}
	if cfg.Tracker.GracePeriod != "0s" {
		t.Errorf("expected Tracker.GracePeriod='0s', got %q", cfg.Tracker.GracePeriod)
	}
	if cfg.Tracker.Quiet == nil || *cfg.Tracker.Quiet {
		t.Errorf("expected Tracker.Quiet=false, got %v", cfg.Tracker.Quiet)
	}
	if cfg.Detection.ManifestsDir != "" {
		t.Errorf("expected empty Detection.ManifestsDir, got %q", cfg.Detection.ManifestsDir)
	}
	if cfg.Detection.ScreenInspection == nil || !*cfg.Detection.ScreenInspection {
		t.Errorf("expected Detection.ScreenInspection=true, got %v", cfg.Detection.ScreenInspection)
	}
}

//nolint:cyclop // integration test verifying EnsureConfigFile lifecycle steps
func TestEnsureConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "subdir", "config.toml")

	// 1. EnsureConfigFile creates new file and parent dirs
	created, err := EnsureConfigFile(configPath)
	if err != nil {
		t.Fatalf("EnsureConfigFile failed: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for new file")
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != DefaultConfigTemplate() {
		t.Fatalf("file content does not match template")
	}

	// 2. EnsureConfigFile on existing file returns created=false, nil without modifying
	customContent := "# custom content"
	if err := os.WriteFile(configPath, []byte(customContent), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = EnsureConfigFile(configPath)
	if err != nil {
		t.Fatalf("EnsureConfigFile on existing file failed: %v", err)
	}
	if created {
		t.Fatal("expected created=false for existing file")
	}
	reRead, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(reRead) != customContent {
		t.Fatalf("existing file was overwritten: got %q, want %q", string(reRead), customContent)
	}

	// 3. WriteConfigFile overwrites existing file
	if err := WriteConfigFile(configPath); err != nil {
		t.Fatalf("WriteConfigFile failed: %v", err)
	}
	overwritten, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(overwritten) != DefaultConfigTemplate() {
		t.Fatalf("expected DefaultConfigTemplate after WriteConfigFile")
	}

	// 4. EnsureConfigFile on directory path returns error
	dirPath := filepath.Join(tempDir, "directory")
	if err := os.MkdirAll(dirPath, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = EnsureConfigFile(dirPath)
	if err == nil {
		t.Fatal("expected error when path is directory")
	}

	// 5. EnsureConfigFile("") uses DefaultPath (respecting AHT_CONFIG)
	envPath := filepath.Join(tempDir, "env_config", "config.toml")
	t.Setenv(ConfigEnv, envPath)
	created, err = EnsureConfigFile("")
	if err != nil {
		t.Fatalf("EnsureConfigFile(\"\") failed: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for env path")
	}
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf("file at envPath %s does not exist: %v", envPath, err)
	}
}

func TestSparseConfigPreservesDefaults(t *testing.T) {
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	sparseConfig := filepath.Join(tempDir, "sparse.toml")
	content := `
[ui]
sort = "created"
`
	if err := os.WriteFile(sparseConfig, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load(sparseConfig)
	if err != nil {
		t.Fatalf("Load sparse config failed: %v", err)
	}

	// Specified field updated
	if cfg.UI.Sort != "created" {
		t.Errorf("expected UI.Sort='created', got %q", cfg.UI.Sort)
	}
	// Untouched fields must preserve true defaults
	if cfg.UI.DefaultPresence != "all" {
		t.Errorf("expected UI.DefaultPresence='all', got %q", cfg.UI.DefaultPresence)
	}
	if cfg.Retention.MaxGoneAge != "7d" {
		t.Errorf("expected Retention.MaxGoneAge='7d', got %q", cfg.Retention.MaxGoneAge)
	}
	if cfg.Tracker.Interval != "300ms" {
		t.Errorf("expected Tracker.Interval='300ms', got %q", cfg.Tracker.Interval)
	}
	if cfg.Detection.ScreenInspection == nil || !*cfg.Detection.ScreenInspection {
		t.Errorf("expected Detection.ScreenInspection=true, got %v", cfg.Detection.ScreenInspection)
	}
}

func TestLoadWithOptionsFilePrecedence(t *testing.T) {
	dir := t.TempDir()
	sysDir := filepath.Join(dir, "system")
	userDir := filepath.Join(dir, "user")
	explicitPath := filepath.Join(dir, "config.toml")
	t.Setenv(ConfigEnv, "")
	t.Setenv("XDG_CONFIG_DIRS", sysDir)
	t.Setenv("XDG_CONFIG_HOME", userDir)
	for _, path := range []string{filepath.Join(sysDir, "aht"), filepath.Join(userDir, "aht")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sysDir, "aht", "config.toml"), []byte("[ui]\ntime_format = 'iso8601'\nsort = 'updated'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "aht", "config.toml"), []byte("[ui]\nsort = 'cwd'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicitPath, []byte("[ui]\ndefault_presence = 'live'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, resolved, err := LoadWithMetadata(Options{Path: explicitPath, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != explicitPath || cfg.UI.Sort != "cwd" || cfg.UI.DefaultPresence != "live" || cfg.UI.TimeFormat != "iso8601" {
		t.Fatalf("layers: path=%q, config=%+v", resolved, cfg.UI)
	}
	assertLayerOrigins(t, meta)
}

func assertLayerOrigins(t *testing.T, meta *strata.Metadata) {
	t.Helper()
	for _, tc := range []struct {
		key    string
		source strata.SourceKind
	}{
		{key: "ui.time_format", source: strata.SourceSystem},
		{key: "ui.sort", source: strata.SourceUser},
		{key: "ui.default_presence", source: strata.SourceFile},
	} {
		origin, ok := meta.Where(tc.key)
		if !ok || origin.Source != tc.source {
			t.Errorf("%s origin = %+v, found %t", tc.key, origin, ok)
		}
	}
}

func TestProjectFileRequiresExplicitPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ConfigEnv, "")
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(dir, "system"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "user"))
	t.Chdir(dir)
	path := filepath.Join(dir, ".aht.toml")
	if err := os.WriteFile(path, []byte("[ui]\nsort = 'created'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, meta, _, err := LoadWithMetadata(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Sort != "updated" || len(meta.ActiveFiles()) != 0 {
		t.Fatalf("implicit project file affected config: %+v, files = %v", cfg.UI, meta.ActiveFiles())
	}
	cfg, meta, resolved, err := LoadWithMetadata(Options{Path: path, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Sort != "created" || resolved != path {
		t.Fatalf("explicit file: sort=%q, path=%q", cfg.UI.Sort, resolved)
	}
	if origin, ok := meta.Where("ui.sort"); !ok || origin.Source != strata.SourceFile {
		t.Fatalf("explicit origin = %+v, found %t", origin, ok)
	}
}

func TestLoadWithOptionsStdin(t *testing.T) {
	isolateConfigEnv(t)
	stdinContent := `
[ui]
sort = "activity"
default_presence = "unknown"
`
	cfg, resolved, err := LoadWithOptions(Options{
		Path:  "-",
		Stdin: strings.NewReader(stdinContent),
	})
	if err != nil {
		t.Fatalf("LoadWithOptions stdin failed: %v", err)
	}
	if resolved != "-" {
		t.Errorf("expected resolved '-', got %q", resolved)
	}
	if cfg.UI.Sort != "activity" {
		t.Errorf("expected sort='activity', got %q", cfg.UI.Sort)
	}
	if cfg.UI.DefaultPresence != "unknown" {
		t.Errorf("expected default_presence='unknown', got %q", cfg.UI.DefaultPresence)
	}
	// Untouched defaults preserved
	if cfg.Tracker.Interval != "300ms" {
		t.Errorf("expected tracker.interval='300ms', got %q", cfg.Tracker.Interval)
	}
}

func TestUserConfigDirAndDefaultPath(t *testing.T) {
	tempDir := t.TempDir()
	xdgDir := filepath.Join(tempDir, "xdg_config")
	homeDir := filepath.Join(tempDir, "home")

	t.Run("explicit AHT_CONFIG overrides default path", func(t *testing.T) {
		customPath := filepath.Join(tempDir, "custom", "config.toml")
		t.Setenv(ConfigEnv, customPath)
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		t.Setenv("HOME", homeDir)

		if got := DefaultPath(); got != customPath {
			t.Fatalf("DefaultPath() = %q, want %q", got, customPath)
		}
	})

	t.Run("XDG_CONFIG_HOME takes precedence when AHT_CONFIG unset", func(t *testing.T) {
		t.Setenv(ConfigEnv, "")
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		t.Setenv("HOME", homeDir)

		if got := UserConfigDir(); got != xdgDir {
			t.Fatalf("UserConfigDir() = %q, want %q", got, xdgDir)
		}
		expectedPath := filepath.Join(xdgDir, "aht", "config.toml")
		if got := DefaultPath(); got != expectedPath {
			t.Fatalf("DefaultPath() = %q, want %q", got, expectedPath)
		}
	})

	t.Run("existing user YAML is ignored", func(t *testing.T) {
		t.Setenv(ConfigEnv, "")
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		path := filepath.Join(xdgDir, "aht", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ui:\n  sort: created\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		expectedPath := filepath.Join(xdgDir, "aht", "config.toml")
		if got := DefaultPath(); got != expectedPath {
			t.Fatalf("DefaultPath() = %q, want %q", got, expectedPath)
		}
	})

	t.Run("HOME/.config is used when XDG_CONFIG_HOME is unset", func(t *testing.T) {
		t.Setenv(ConfigEnv, "")
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", homeDir)

		expectedDir := filepath.Join(homeDir, ".config")
		if got := UserConfigDir(); got != expectedDir {
			t.Fatalf("UserConfigDir() = %q, want %q", got, expectedDir)
		}
		expectedPath := filepath.Join(homeDir, ".config", "aht", "config.toml")
		if got := DefaultPath(); got != expectedPath {
			t.Fatalf("DefaultPath() = %q, want %q", got, expectedPath)
		}
	})
}

func TestOnlyTOMLConfigFiles(t *testing.T) {
	isolateConfigEnv(t)
	userBase := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", userBase)
	userDir := filepath.Join(userBase, "aht")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".yaml", ".json"} {
		path := filepath.Join(userDir, "config"+ext)
		if err := os.WriteFile(path, []byte("[ui]\nsort = \"created\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Load(path); !errors.Is(err, strata.ErrUnsupportedFormat) {
			t.Fatalf("Load(%q) error = %v, want unsupported format", path, err)
		}
		if err := WriteConfigFile(path); !errors.Is(err, strata.ErrUnsupportedFormat) {
			t.Fatalf("WriteConfigFile(%q) error = %v, want unsupported format", path, err)
		}
		if _, err := EnsureConfigFile(path); !errors.Is(err, strata.ErrUnsupportedFormat) {
			t.Fatalf("EnsureConfigFile(%q) error = %v, want unsupported format", path, err)
		}
	}
	cfg, resolved, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Sort != "updated" || resolved != filepath.Join(userDir, "config.toml") {
		t.Fatalf("config sort = %q, path = %q", cfg.UI.Sort, resolved)
	}
	t.Setenv(ConfigEnv, filepath.Join(userDir, "config.yaml"))
	if _, _, err := Load(""); !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("AHT_CONFIG YAML error = %v, want unsupported format", err)
	}
	t.Setenv(ConfigEnv, filepath.Join(userDir, "missing.yaml"))
	if _, _, err := Load(""); !errors.Is(err, strata.ErrUnsupportedFormat) {
		t.Fatalf("missing AHT_CONFIG YAML error = %v, want unsupported format", err)
	}
}

func TestTOMLExtensionMatchesStrataSelection(t *testing.T) {
	isolateConfigEnv(t)
	path := filepath.Join(t.TempDir(), "config.TOML")
	created, err := EnsureConfigFile(path)
	if err != nil || !created {
		t.Fatalf("EnsureConfigFile(%q) = %t, %v", path, created, err)
	}
	if err := WriteConfigFile(path); err != nil {
		t.Fatal(err)
	}
	cfg, resolved, err := Load(path)
	if err != nil || resolved != path || cfg.UI.Sort != "updated" {
		t.Fatalf("Load(%q): sort = %q, resolved = %q, error = %v", path, cfg.UI.Sort, resolved, err)
	}
}
