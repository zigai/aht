package config

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

const (
	// ConfigEnv is the environment variable for overriding the config file path.
	ConfigEnv = "AHT_CONFIG"

	// maxConfigFileSize caps configuration files to 1 MiB.
	maxConfigFileSize = 1024 * 1024

	defaultDirMode  = 0o700
	defaultFileMode = 0o600
)

// Sentinel configuration errors.
var (
	ErrMissingUnitSuffix   = errors.New("missing unit suffix")
	ErrInvalidSort         = errors.New("invalid ui.sort")
	ErrInvalidPresence     = errors.New("invalid ui.default_presence")
	ErrInvalidTimeFormat   = errors.New("invalid ui.time_format")
	ErrNegativeAge         = errors.New("retention.max_gone_age must be non-negative")
	ErrNonPositiveInterval = errors.New("tracker.interval must be positive")
	ErrNegativeGracePeriod = errors.New("tracker.grace_period must be non-negative")
	ErrConfigIsDirectory   = errors.New("config path is a directory")
	ErrConfigFileTooLarge  = errors.New("config file exceeds 1 MiB limit")
	ErrConfigNotFound      = errors.New("config file not found")
	ErrParseConfig         = errors.New("failed to parse config file")
	ErrLoadDefaults        = errors.New("failed to load base defaults")
	ErrLoadEnv             = errors.New("failed to load environment overrides")
	ErrUnmarshalConfig     = errors.New("failed to unmarshal configuration")
	ErrAccessConfig        = errors.New("failed to access config file")
	ErrInvalidDuration     = errors.New("invalid duration")

	validSortKeys = map[string]string{
		"updated":             "updated",
		"time":                "updated",
		"updated-at":          "updated",
		"created":             "created",
		"harness":             "harness",
		"agent":               "harness",
		"presence":            "presence",
		"activity":            "activity",
		"cwd":                 "cwd",
		"id":                  "id",
		"multiplexer":         "multiplexer",
		"mux":                 "multiplexer",
		"tmux":                "tmux",
		"presence-changed":    "presence-changed",
		"presence-changed-at": "presence-changed",
		"presence-since":      "presence-changed",
		"activity-changed":    "activity-changed",
		"activity-changed-at": "activity-changed",
		"activity-since":      "activity-changed",
	}
)

type Config struct {
	UI        UIConfig        `json:"ui"        toml:"ui"`
	Retention RetentionConfig `json:"retention" toml:"retention"`
	Filter    FilterConfig    `json:"filter"    toml:"filter"`
	Tracker   TrackerConfig   `json:"tracker"   toml:"tracker"`
	Detection DetectionConfig `json:"detection" toml:"detection"`
}

// UIConfig controls terminal and table display defaults.
type UIConfig struct {
	DefaultPresence string `json:"default_presence,omitempty" toml:"default_presence"`
	Sort            string `json:"sort,omitempty"             toml:"sort"`
	SortDesc        *bool  `json:"sort_desc,omitempty"        toml:"sort_desc"`
	AbsoluteTime    *bool  `json:"absolute_time,omitempty"    toml:"absolute_time"`
	TimeFormat      string `json:"time_format,omitempty"      toml:"time_format"`
}

// RetentionConfig controls state retention and tombstone cleanup defaults.
type RetentionConfig struct {
	AutoClean  *bool  `json:"auto_clean,omitempty"   toml:"auto_clean"`
	MaxGoneAge string `json:"max_gone_age,omitempty" toml:"max_gone_age"`
}

// FilterConfig controls default session visibility exclusions.
type FilterConfig struct {
	IgnoreHarnesses []string `json:"ignore_harnesses,omitempty" toml:"ignore_harnesses"`
	IgnorePaths     []string `json:"ignore_paths,omitempty"     toml:"ignore_paths"`
}

// TrackerConfig controls background observer behavior.
type TrackerConfig struct {
	Interval    string `json:"interval,omitempty"     toml:"interval"`
	GracePeriod string `json:"grace_period,omitempty" toml:"grace_period"`
	Quiet       *bool  `json:"quiet,omitempty"        toml:"quiet"`
}

// DetectionConfig controls agent and screen inspection defaults.
type DetectionConfig struct {
	ManifestsDir     string `json:"manifests_dir,omitempty"     toml:"manifests_dir"`
	ScreenInspection *bool  `json:"screen_inspection,omitempty" toml:"screen_inspection"`
}

// Options controls layered configuration resolution.
type Options struct {
	Path          string
	Explicit      bool
	NoConfig      bool
	Stdin         io.Reader
	CWD           string
	UserConfigDir string
	SystemDirs    []string
}

// Defaults returns a complete typed configuration with all base defaults populated.
func Defaults() Config {
	return Config{
		UI: UIConfig{
			DefaultPresence: "all",
			Sort:            "updated",
			SortDesc:        new(false),
			AbsoluteTime:    new(false),
			TimeFormat:      "relative",
		},
		Retention: RetentionConfig{
			AutoClean:  new(false),
			MaxGoneAge: "7d",
		},
		Filter: FilterConfig{
			IgnoreHarnesses: []string{},
			IgnorePaths:     []string{},
		},
		Tracker: TrackerConfig{
			Interval:    "300ms",
			GracePeriod: "0s",
			Quiet:       new(false),
		},
		Detection: DetectionConfig{
			ManifestsDir:     "",
			ScreenInspection: new(true),
		},
	}
}

// DefaultPath returns the default path to the user's config file.
func DefaultPath() string {
	if val := strings.TrimSpace(os.Getenv(ConfigEnv)); val != "" {
		return val
	}
	configDir, err := os.UserConfigDir()
	if err == nil && configDir != "" {
		return filepath.Join(configDir, "aht", "config.toml")
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "aht", "config.toml")
	}
	return filepath.Join(os.TempDir(), "aht", "config.toml")
}

// DefaultConfigTemplate returns a formatted TOML string containing every default
// option with descriptive comments.
func DefaultConfigTemplate() string {
	return `# AHT Configuration (config.toml)
# Track local coding-agent sessions and where they are running.
# Override location with $AHT_CONFIG or --config <path>.

[ui]
# Default session presence filter: "live", "gone", "unknown", or "all"
default_presence = "all"

# Sort sessions by: "updated", "created", "harness", "presence", "activity", "cwd", "id", "multiplexer", "tmux"
sort = "updated"

# Sort in descending order
sort_desc = false

# Display timestamps in absolute format rather than relative
absolute_time = false

# Timestamp display format: "relative", "absolute", or "iso8601"
time_format = "relative"

[retention]
# Automatically clean up expired gone sessions in background tracker
auto_clean = false

# Maximum age of gone sessions before tombstone cleanup, e.g. "7d", "24h"
max_gone_age = "7d"

[filter]
# List of harnesses to omit from default session listings unless explicitly requested via --agent
ignore_harnesses = []

# List of glob or directory path patterns matching session working directories to ignore
ignore_paths = []

[tracker]
# Reconciliation polling frequency
interval = "300ms"

# Process absence grace period before marking sessions gone
grace_period = "0s"

# Suppress human cycle output and diagnostics from tracker
quiet = false

[detection]
# Directory containing custom agent screen detection manifest TOML files
manifests_dir = ""

# Enable terminal multiplexer screen inspection
screen_inspection = true
`
}

// WriteConfigFile writes the default configuration template to path, creating
// parent directories if needed. It overwrites any existing file at path.
func WriteConfigFile(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	_, err := publishConfigFile(path, true)
	return err
}

// EnsureConfigFile ensures that a configuration file exists at path.
// If path is empty, DefaultPath() is used.
// If the file already exists, it returns created=false, nil.
// If the file does not exist, it creates the parent directories and writes DefaultConfigTemplate()
// with 0o600 permissions, returning created=true, nil. Publication is atomic and
// never overwrites a file created by another process.
func EnsureConfigFile(path string) (bool, error) {
	if path == "" {
		path = DefaultPath()
	}
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return false, fmt.Errorf("%w: %s", ErrConfigIsDirectory, path)
		}
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%w %s: %w", ErrAccessConfig, path, err)
	}

	return publishConfigFile(path, false)
}

// ParseDuration parses duration strings including day suffixes (e.g. "7d", "24h", "10s").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") || strings.HasSuffix(s, "D") {
		valStr := strings.TrimSpace(s[:len(s)-1])
		days, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q: %w", s, err)
		}
		const day = 24 * time.Hour
		if days > math.MaxInt64/int64(day) || days < math.MinInt64/int64(day) {
			return 0, fmt.Errorf("%w: day duration %q is out of range", ErrInvalidDuration, s)
		}
		return time.Duration(days) * day, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		if _, numErr := strconv.Atoi(s); numErr == nil {
			return 0, fmt.Errorf("%w: %q (e.g. %q or %q)", ErrMissingUnitSuffix, s, s+"d", s+"h")
		}
		return 0, fmt.Errorf("%w: %w", ErrInvalidDuration, err)
	}
	return d, nil
}

// NormalizeSort validates and normalizes sort key names.
func NormalizeSort(s string) (string, error) {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"))
	if key, ok := validSortKeys[norm]; ok {
		return key, nil
	}
	return "", fmt.Errorf("%w %q, must be one of: updated, created, harness, presence, activity, cwd, id, multiplexer, tmux, presence-changed, activity-changed", ErrInvalidSort, s)
}

// Validate checks configuration invariants.
func (c Config) Validate() error {
	if err := c.validateUI(); err != nil {
		return err
	}
	if err := c.validateRetention(); err != nil {
		return err
	}
	return c.validateTracker()
}

func (c Config) validateUI() error {
	if c.UI.DefaultPresence != "" {
		trimmed := strings.ToLower(strings.TrimSpace(c.UI.DefaultPresence))
		if trimmed != "all" {
			if _, err := registry.NormalizePresence(trimmed); err != nil {
				return fmt.Errorf("%w %q (allowed: live, gone, unknown, all): %w", ErrInvalidPresence, c.UI.DefaultPresence, err)
			}
		}
	}
	if c.UI.Sort != "" {
		if _, err := NormalizeSort(c.UI.Sort); err != nil {
			return err
		}
	}
	if c.UI.TimeFormat != "" {
		tf := strings.ToLower(strings.TrimSpace(c.UI.TimeFormat))
		if tf != "relative" && tf != "absolute" && tf != "iso8601" {
			return fmt.Errorf("%w %q (allowed: relative, absolute, iso8601)", ErrInvalidTimeFormat, c.UI.TimeFormat)
		}
	}
	return nil
}

func (c Config) validateRetention() error {
	if c.Retention.MaxGoneAge != "" {
		d, err := ParseDuration(c.Retention.MaxGoneAge)
		if err != nil {
			return fmt.Errorf("invalid retention.max_gone_age %q: %w", c.Retention.MaxGoneAge, err)
		}
		if d < 0 {
			return ErrNegativeAge
		}
	}
	return nil
}

func (c Config) validateTracker() error {
	if c.Tracker.Interval != "" {
		d, err := ParseDuration(c.Tracker.Interval)
		if err != nil {
			return fmt.Errorf("invalid tracker.interval %q: %w", c.Tracker.Interval, err)
		}
		if d <= 0 {
			return ErrNonPositiveInterval
		}
	}
	if c.Tracker.GracePeriod != "" {
		d, err := ParseDuration(c.Tracker.GracePeriod)
		if err != nil {
			return fmt.Errorf("invalid tracker.grace_period %q: %w", c.Tracker.GracePeriod, err)
		}
		if d < 0 {
			return ErrNegativeGracePeriod
		}
	}
	return nil
}

// Load loads, parses, and validates the configuration file from path.
// If path is empty, DefaultPath() is used.
// A missing default config file is silently skipped; a missing explicitly specified path is an error.
func Load(path string) (Config, string, error) {
	return LoadWithOptions(Options{Path: path, Explicit: path != ""})
}

// LoadWithOptions resolves configuration across all tiers per the given options.
//
//nolint:gocognit,cyclop,nestif // layered configuration resolution across 6 tiers
func LoadWithOptions(opts Options) (Config, string, error) {
	cfg := Defaults()
	var resolvedPath string

	if opts.NoConfig {
		if err := applyEnvOverrides(&cfg); err != nil {
			return Config{}, "", fmt.Errorf("%w: %w", ErrLoadEnv, err)
		}
		norm, err := normalizeConfig(cfg)
		return norm, "", err
	}

	explicit := opts.Explicit
	targetPath := opts.Path
	if targetPath == "" {
		targetPath = strings.TrimSpace(os.Getenv(ConfigEnv))
	}

	if targetPath != "" {
		switch {
		case targetPath == "-":
			r := opts.Stdin
			if r == nil {
				r = os.Stdin
			}
			contents, err := readBounded(r, maxConfigFileSize)
			if err != nil {
				if errors.Is(err, ErrConfigFileTooLarge) {
					return Config{}, "-", fmt.Errorf("%w: stdin", ErrConfigFileTooLarge)
				}
				return Config{}, "-", err
			}
			if err := decodeTOML(contents, &cfg); err != nil {
				return Config{}, "-", fmt.Errorf("%w stdin: %w", ErrParseConfig, err)
			}
			resolvedPath = "-"
		case explicit:
			cleanTarget := filepath.Clean(targetPath)
			info, err := os.Stat(cleanTarget)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return Config{}, targetPath, fmt.Errorf("%w %s: %w", ErrConfigNotFound, targetPath, err)
				}
				return Config{}, targetPath, fmt.Errorf("%w %s: %w", ErrAccessConfig, targetPath, err)
			}
			if info.IsDir() {
				return Config{}, targetPath, fmt.Errorf("%w: %s", ErrConfigIsDirectory, targetPath)
			}
			if info.Size() > maxConfigFileSize {
				return Config{}, targetPath, fmt.Errorf("%w (%d bytes): %s", ErrConfigFileTooLarge, info.Size(), targetPath)
			}
			contents, err := readBoundedFile(cleanTarget)
			if err != nil {
				return Config{}, targetPath, err
			}
			if err := decodeTOML(contents, &cfg); err != nil {
				return Config{}, targetPath, fmt.Errorf("%w %s: %w", ErrParseConfig, targetPath, err)
			}
			resolvedPath = targetPath
		default:
			resolvedPath = targetPath
			cleanTarget := filepath.Clean(targetPath)
			info, err := os.Stat(cleanTarget)
			if err == nil {
				if info.IsDir() {
					return Config{}, targetPath, fmt.Errorf("%w: %s", ErrConfigIsDirectory, targetPath)
				}
				if info.Size() > maxConfigFileSize {
					return Config{}, targetPath, fmt.Errorf("%w (%d bytes): %s", ErrConfigFileTooLarge, info.Size(), targetPath)
				}
				contents, err := readBoundedFile(cleanTarget)
				if err != nil {
					return Config{}, targetPath, err
				}
				if err := decodeTOML(contents, &cfg); err != nil {
					return Config{}, targetPath, fmt.Errorf("%w %s: %w", ErrParseConfig, targetPath, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return Config{}, targetPath, fmt.Errorf("%w %s: %w", ErrAccessConfig, targetPath, err)
			}
		}
	} else {
		// Discovered mode across 3 disk tiers: System -> User -> Project
		// 1. System tier (earlier entries in systemDirs take precedence)
		systemDirs := opts.SystemDirs
		if len(systemDirs) == 0 {
			systemDirs = defaultSystemConfigDirs()
		}
		for _, baseDir := range slices.Backward(systemDirs) {
			sysPath := filepath.Join(baseDir, "aht", "config.toml")
			if err := loadDiskOverlay(sysPath, &cfg); err != nil {
				return Config{}, sysPath, err
			}
		}

		// 2. User tier
		userPath := opts.UserConfigDir
		if userPath == "" {
			userPath = DefaultPath()
		} else if !strings.HasSuffix(userPath, ".toml") {
			userPath = filepath.Join(userPath, "aht", "config.toml")
		}
		resolvedPath = userPath
		if err := loadDiskOverlay(userPath, &cfg); err != nil {
			return Config{}, userPath, err
		}

		// 3. Project tier (.aht.toml in CWD)
		cwd := opts.CWD
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		if cwd != "" {
			projectPath := filepath.Join(cwd, ".aht.toml")
			cleanProj := filepath.Clean(projectPath)
			info, err := os.Stat(cleanProj)
			if err == nil && !info.IsDir() {
				if err := loadDiskOverlay(projectPath, &cfg); err != nil {
					return Config{}, projectPath, err
				}
				resolvedPath = projectPath
			}
		}
	}

	// Layer 2: Environment variable overrides
	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, resolvedPath, fmt.Errorf("%w: %w", ErrLoadEnv, err)
	}

	norm, err := normalizeConfig(cfg)
	return norm, resolvedPath, err
}

func loadDiskOverlay(path string, target *Config) error {
	cleanPath := filepath.Clean(path)
	info, err := os.Stat(cleanPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%w %s: %w", ErrAccessConfig, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%w: %s", ErrConfigIsDirectory, path)
	}
	if info.Size() > maxConfigFileSize {
		return fmt.Errorf("%w (%d bytes): %s", ErrConfigFileTooLarge, info.Size(), path)
	}
	contents, err := readBoundedFile(cleanPath)
	if err != nil {
		return err
	}
	if err := decodeTOML(contents, target); err != nil {
		return fmt.Errorf("%w %s: %w", ErrParseConfig, path, err)
	}
	return nil
}

func defaultSystemConfigDirs() []string {
	if runtime.GOOS == "windows" {
		if progData := os.Getenv("ProgramData"); progData != "" {
			return []string{progData}
		}
		return nil
	}
	xdgDirs := os.Getenv("XDG_CONFIG_DIRS")
	if xdgDirs == "" {
		return []string{"/etc/xdg"}
	}
	var clean []string
	for d := range strings.SplitSeq(xdgDirs, ":") {
		if trimmed := strings.TrimSpace(d); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	return clean
}

//nolint:gocognit,cyclop // straightforward mapping of environment variables to config fields
func applyEnvOverrides(cfg *Config) error {
	if v, ok := os.LookupEnv("AHT_UI_DEFAULT_PRESENCE"); ok {
		cfg.UI.DefaultPresence = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_UI_SORT"); ok {
		cfg.UI.Sort = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_UI_SORT_DESC"); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("invalid AHT_UI_SORT_DESC %q: %w", v, err)
		}
		cfg.UI.SortDesc = new(b)
	}
	if v, ok := os.LookupEnv("AHT_UI_ABSOLUTE_TIME"); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("invalid AHT_UI_ABSOLUTE_TIME %q: %w", v, err)
		}
		cfg.UI.AbsoluteTime = new(b)
	}
	if v, ok := os.LookupEnv("AHT_UI_TIME_FORMAT"); ok {
		cfg.UI.TimeFormat = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_RETENTION_AUTO_CLEAN"); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("invalid AHT_RETENTION_AUTO_CLEAN %q: %w", v, err)
		}
		cfg.Retention.AutoClean = new(b)
	}
	if v, ok := os.LookupEnv("AHT_RETENTION_MAX_GONE_AGE"); ok {
		cfg.Retention.MaxGoneAge = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_FILTER_IGNORE_HARNESSES"); ok {
		cfg.Filter.IgnoreHarnesses = parseEnvList(v)
	}
	if v, ok := os.LookupEnv("AHT_FILTER_IGNORE_PATHS"); ok {
		cfg.Filter.IgnorePaths = parseEnvList(v)
	}
	if v, ok := os.LookupEnv("AHT_TRACKER_INTERVAL"); ok {
		cfg.Tracker.Interval = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_TRACKER_GRACE_PERIOD"); ok {
		cfg.Tracker.GracePeriod = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_TRACKER_QUIET"); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("invalid AHT_TRACKER_QUIET %q: %w", v, err)
		}
		cfg.Tracker.Quiet = new(b)
	}
	if v, ok := os.LookupEnv("AHT_DETECTION_MANIFESTS_DIR"); ok {
		cfg.Detection.ManifestsDir = strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv("AHT_DETECTION_SCREEN_INSPECTION"); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("invalid AHT_DETECTION_SCREEN_INSPECTION %q: %w", v, err)
		}
		cfg.Detection.ScreenInspection = new(b)
	}
	return nil
}

func parseEnvList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return []string{}
	}
	parts := strings.Split(v, ",")
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			clean = append(clean, t)
		}
	}
	return clean
}

func normalizeConfig(cfg Config) (Config, error) {
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	cfg.UI.DefaultPresence = strings.ToLower(strings.TrimSpace(cfg.UI.DefaultPresence))
	cfg.UI.TimeFormat = strings.ToLower(strings.TrimSpace(cfg.UI.TimeFormat))
	if cfg.UI.Sort != "" {
		var err error
		cfg.UI.Sort, err = NormalizeSort(cfg.UI.Sort)
		if err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}
