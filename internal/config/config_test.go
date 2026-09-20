package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
}

func TestLoadMissingDefaultFile(t *testing.T) {
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "does-not-exist.toml")
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

func TestSettingsIgnoreEnvironment(t *testing.T) {
	for _, key := range []string{
		"AHT_UI_DEFAULT_PRESENCE", "AHT_UI_SORT", "AHT_UI_SORT_DESC",
		"AHT_UI_ABSOLUTE_TIME", "AHT_UI_TIME_FORMAT", "AHT_RETENTION_AUTO_CLEAN",
		"AHT_RETENTION_MAX_GONE_AGE", "AHT_FILTER_IGNORE_HARNESSES", "AHT_FILTER_IGNORE_PATHS",
		"AHT_TRACKER_INTERVAL", "AHT_TRACKER_GRACE_PERIOD", "AHT_TRACKER_QUIET",
		"AHT_DETECTION_MANIFESTS_DIR", "AHT_DETECTION_SCREEN_INSPECTION",
	} {
		t.Setenv(key, "invalid")
	}
	for _, noConfig := range []bool{false, true} {
		cfg, _, err := LoadWithOptions(Options{
			Path:     filepath.Join(t.TempDir(), "missing.toml"),
			NoConfig: noConfig,
		})
		if err != nil {
			t.Fatalf("NoConfig=%v: %v", noConfig, err)
		}
		if !reflect.DeepEqual(cfg, Defaults()) {
			t.Fatalf("NoConfig=%v: environment changed settings: %+v", noConfig, cfg)
		}
	}
}

func TestMaxFileSizeLimit(t *testing.T) {
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
	isolateConfigEnv(t)
	tempDir := t.TempDir()
	sysDir := filepath.Join(tempDir, "sys")
	userDir := filepath.Join(tempDir, "user")
	projectDir := filepath.Join(tempDir, "project")

	for _, d := range []string{filepath.Join(sysDir, "aht"), filepath.Join(userDir, "aht"), projectDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(sysDir, "aht", "config.toml"), []byte("[ui]\ntime_format = \"iso8601\"\nsort = \"updated\"\ndefault_presence = \"unknown\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "aht", "config.toml"), []byte("[ui]\nsort = \"cwd\"\ndefault_presence = \"gone\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Project tier: sets default_presence = "live"
	if err := os.WriteFile(filepath.Join(projectDir, ".aht.toml"), []byte("[ui]\ndefault_presence = \"live\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, resolved, err := LoadWithOptions(Options{
		CWD:           projectDir,
		UserConfigDir: userDir,
		SystemDirs:    []string{sysDir},
	})
	if err != nil {
		t.Fatalf("LoadWithOptions failed: %v", err)
	}
	if resolved != filepath.Join(projectDir, ".aht.toml") {
		t.Errorf("expected resolved path %s, got %s", filepath.Join(projectDir, ".aht.toml"), resolved)
	}

	// Check precedence:
	// User tier provides sort
	if cfg.UI.Sort != "cwd" {
		t.Errorf("expected sort='cwd' from user config, got %q", cfg.UI.Sort)
	}
	// Project tier wins for default_presence
	if cfg.UI.DefaultPresence != "live" {
		t.Errorf("expected default_presence='live' from project, got %q", cfg.UI.DefaultPresence)
	}
	// System tier provides time_format
	if cfg.UI.TimeFormat != "iso8601" {
		t.Errorf("expected time_format='iso8601' from system, got %q", cfg.UI.TimeFormat)
	}
	// Untouched field retains hardcoded default
	if cfg.Retention.MaxGoneAge != "7d" {
		t.Errorf("expected max_gone_age='7d' from defaults, got %q", cfg.Retention.MaxGoneAge)
	}
}

func TestLoadWithOptionsNoConfig(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".aht.toml"), []byte("[ui]\nsort = \"harness\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, resolved, err := LoadWithOptions(Options{
		NoConfig: true,
		CWD:      projectDir,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions NoConfig failed: %v", err)
	}
	if resolved != "" {
		t.Errorf("expected resolved path '', got %q", resolved)
	}
	// Disk project config was skipped
	if cfg.UI.Sort != "updated" {
		t.Errorf("expected default sort='updated', got %q", cfg.UI.Sort)
	}
	// Unconfigured values retain defaults
	if cfg.UI.DefaultPresence != "all" {
		t.Errorf("expected default_presence='all', got %q", cfg.UI.DefaultPresence)
	}
}

func TestLoadWithOptionsStdin(t *testing.T) {
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
