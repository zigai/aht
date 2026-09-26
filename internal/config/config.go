package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/pkg/registry"
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
	ErrNonPositiveTTL      = errors.New("retention.tombstone_ttl must be positive")
	ErrNonPositiveInterval = errors.New("tracker.interval must be positive")
	ErrNegativeGracePeriod = errors.New("tracker.grace_period must be non-negative")
	ErrConfigIsDirectory   = strata.ErrPathIsDirectory
	ErrConfigFileTooLarge  = fmt.Errorf("%w: config file exceeds 1 MiB limit", strata.ErrFileTooLarge)
	ErrConfigNotFound      = errors.New("config file not found")
	ErrParseConfig         = errors.New("failed to parse config file")
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

// RetentionConfig controls how long the tracker keeps gone-session tombstones.
type RetentionConfig struct {
	// TombstoneTTL is how long an identified gone session stays in the registry
	// so late native reports from its ended process are rejected. Gone sessions
	// with only process identity are removed immediately.
	TombstoneTTL string `json:"tombstone_ttl,omitempty" toml:"tombstone_ttl"`

	// AutoClean is accepted so existing config files keep loading. It is
	// ignored: the tracker always expires tombstones after TombstoneTTL.
	//
	// Deprecated: use TombstoneTTL.
	AutoClean *bool `json:"-" toml:"auto_clean"`
	// MaxGoneAge is accepted so existing config files keep loading. It is
	// ignored.
	//
	// Deprecated: use TombstoneTTL.
	MaxGoneAge string `json:"-" toml:"max_gone_age"`
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
	Path     string
	Explicit bool
	NoConfig bool
	Stdin    io.Reader
}

// SetDefaults declares the configuration defaults for strata.Defaulter.
func (c *Config) SetDefaults() {
	*c = Defaults()
}

// ValidateWith checks configuration invariants and normalizes canonical fields.
// It implements strata.MetadataValidator.
func (c *Config) ValidateWith(meta *strata.Metadata) error {
	if err := c.validateUI(meta); err != nil {
		return err
	}
	if err := c.validateRetention(meta); err != nil {
		return err
	}
	return c.validateTracker(meta)
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
			TombstoneTTL: "10m",
			AutoClean:    nil,
			MaxGoneAge:   "",
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

// UserConfigDir returns the base directory for user configuration.
// It prioritizes $XDG_CONFIG_HOME, then $HOME/.config (consistent across Linux
// and macOS), before falling back to the operating system's user config directory.
func UserConfigDir() string {
	if val := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); val != "" {
		return val
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return dir
	}
	return ""
}

// DefaultPath returns the default path to the user's config file.
func DefaultPath() string {
	if val := strings.TrimSpace(os.Getenv(ConfigEnv)); val != "" {
		return val
	}
	if path, err := strata.ConfigEditPath(strata.WithAppName("aht"), strata.WithFormats(".toml")); err == nil {
		return path
	}
	if dir := UserConfigDir(); dir != "" {
		return filepath.Join(dir, "aht", "config.toml")
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
# How long the tracker keeps an ended session that has a native session id, so
# late hook reports cannot revive it, e.g. "10m", "1h". Ended sessions known
# only by their process are removed immediately. Conversation history is
# searched with aht search, not kept in the registry.
tombstone_ttl = "10m"

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
	if err := validateTOMLPath(path); err != nil {
		return err
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
	if err := validateTOMLPath(path); err != nil {
		return false, err
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
	d, err := strata.ParseDuration(s)
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
func (c *Config) Validate() error {
	return c.ValidateWith(nil)
}

func (c *Config) validateUI(meta *strata.Metadata) error {
	if err := c.validatePresence(meta); err != nil {
		return err
	}
	if err := c.validateSort(meta); err != nil {
		return err
	}
	return c.validateTimeFormat(meta)
}

func (c *Config) validatePresence(meta *strata.Metadata) error {
	trimmed := strings.ToLower(strings.TrimSpace(c.UI.DefaultPresence))
	c.UI.DefaultPresence = trimmed
	if trimmed == "" || trimmed == "all" {
		return nil
	}
	if _, err := registry.NormalizePresence(trimmed); err != nil {
		valErr := fmt.Errorf("%w %q (allowed: live, gone, unknown, all): %w", ErrInvalidPresence, c.UI.DefaultPresence, err)
		if meta != nil {
			return meta.NewConfigError("ui.default_presence", valErr)
		}
		return valErr
	}
	return nil
}

func (c *Config) validateSort(meta *strata.Metadata) error {
	if c.UI.Sort == "" {
		return nil
	}
	norm, err := NormalizeSort(c.UI.Sort)
	if err != nil {
		if meta != nil {
			return meta.NewConfigError("ui.sort", err)
		}
		return err
	}
	c.UI.Sort = norm
	return nil
}

func (c *Config) validateTimeFormat(meta *strata.Metadata) error {
	tf := strings.ToLower(strings.TrimSpace(c.UI.TimeFormat))
	c.UI.TimeFormat = tf
	if tf == "" || tf == "relative" || tf == "absolute" || tf == "iso8601" {
		return nil
	}
	valErr := fmt.Errorf("%w %q (allowed: relative, absolute, iso8601)", ErrInvalidTimeFormat, c.UI.TimeFormat)
	if meta != nil {
		return meta.NewConfigError("ui.time_format", valErr)
	}
	return valErr
}

func (c *Config) validateRetention(meta *strata.Metadata) error {
	if c.Retention.TombstoneTTL == "" {
		return nil
	}
	d, err := ParseDuration(c.Retention.TombstoneTTL)
	if err != nil {
		valErr := fmt.Errorf("invalid retention.tombstone_ttl %q: %w", c.Retention.TombstoneTTL, err)
		if meta != nil {
			return meta.NewConfigError("retention.tombstone_ttl", valErr)
		}
		return valErr
	}
	if d <= 0 {
		if meta != nil {
			return meta.NewConfigError("retention.tombstone_ttl", ErrNonPositiveTTL)
		}
		return ErrNonPositiveTTL
	}
	return nil
}

func (c *Config) validateTracker(meta *strata.Metadata) error {
	if err := c.validateInterval(meta); err != nil {
		return err
	}
	return c.validateGracePeriod(meta)
}

func (c *Config) validateInterval(meta *strata.Metadata) error {
	if c.Tracker.Interval == "" {
		return nil
	}
	d, err := ParseDuration(c.Tracker.Interval)
	if err != nil {
		valErr := fmt.Errorf("invalid tracker.interval %q: %w", c.Tracker.Interval, err)
		if meta != nil {
			return meta.NewConfigError("tracker.interval", valErr)
		}
		return valErr
	}
	if d <= 0 {
		if meta != nil {
			return meta.NewConfigError("tracker.interval", ErrNonPositiveInterval)
		}
		return ErrNonPositiveInterval
	}
	return nil
}

func (c *Config) validateGracePeriod(meta *strata.Metadata) error {
	if c.Tracker.GracePeriod == "" {
		return nil
	}
	d, err := ParseDuration(c.Tracker.GracePeriod)
	if err != nil {
		valErr := fmt.Errorf("invalid tracker.grace_period %q: %w", c.Tracker.GracePeriod, err)
		if meta != nil {
			return meta.NewConfigError("tracker.grace_period", valErr)
		}
		return valErr
	}
	if d < 0 {
		if meta != nil {
			return meta.NewConfigError("tracker.grace_period", ErrNegativeGracePeriod)
		}
		return ErrNegativeGracePeriod
	}
	return nil
}

// TombstoneTTL returns the configured tombstone TTL, or zero when unset.
func TombstoneTTL(cfg Config) (time.Duration, error) {
	if cfg.Retention.TombstoneTTL == "" {
		return 0, nil
	}
	return ParseDuration(cfg.Retention.TombstoneTTL)
}

// Load loads, parses, and validates the configuration file from path.
func Load(path string) (Config, string, error) {
	return LoadWithOptions(Options{Path: path, Explicit: path != ""})
}

// LoadWithOptions resolves configuration across all tiers per the given options.
func LoadWithOptions(opts Options) (Config, string, error) {
	cfg, _, resolved, err := LoadWithMetadata(opts)
	return cfg, resolved, err
}

// LoadWithMetadata resolves configuration across all tiers per the given options and returns strata.Metadata.
func LoadWithMetadata(opts Options) (Config, *strata.Metadata, string, error) {
	strataOpts, targetPath := strataLoadOptions(opts)
	cfg, meta, err := strata.LoadWithMetadata[Config](strataOpts...)
	resolvedPath := resolveConfigPath(opts, targetPath, meta)
	if err != nil {
		return Config{}, nil, resolvedPath, classifyLoadError(err, opts, targetPath, resolvedPath)
	}
	return cfg, meta, resolvedPath, nil
}

func strataLoadOptions(opts Options) ([]strata.Option, string) {
	strataOpts := []strata.Option{
		strata.WithAppName("aht"),
		strata.WithEnvPrefix("AHT_"),
		strata.WithFormats(".toml"),
		strata.WithMaxFileSize(maxConfigFileSize),
		strata.WithStrict(),
	}
	if opts.NoConfig {
		return append(strataOpts, strata.WithoutFiles()), ""
	}

	targetPath := opts.Path
	if targetPath == "" {
		targetPath = strings.TrimSpace(os.Getenv(ConfigEnv))
	}
	if targetPath == "" {
		return strataOpts, ""
	}
	if opts.Explicit {
		strataOpts = append(strataOpts, strata.WithPath(targetPath))
	} else {
		strataOpts = append(strataOpts, strata.WithOptionalPath(targetPath))
	}
	if targetPath == "-" && opts.Stdin != nil {
		strataOpts = append(strataOpts, strata.WithStdin(opts.Stdin))
	}

	return strataOpts, targetPath
}

func validateTOMLPath(path string) error {
	_, err := strata.ConfigEditPath(strata.WithPath(path), strata.WithFormats(".toml"))
	if err != nil {
		return fmt.Errorf("select TOML config path %s: %w", path, err)
	}
	return nil
}

func resolveConfigPath(opts Options, targetPath string, meta *strata.Metadata) string {
	if opts.NoConfig {
		return ""
	}
	resolvedPath := DefaultPath()
	if targetPath != "" {
		resolvedPath = targetPath
	}
	if meta != nil {
		if active := meta.ActiveFiles(); len(active) > 0 {
			resolvedPath = active[len(active)-1]
		}
	}
	return resolvedPath
}

func classifyLoadError(err error, opts Options, targetPath, resolvedPath string) error {
	if errors.Is(err, strata.ErrFileTooLarge) {
		return fmt.Errorf("%w: %s", ErrConfigFileTooLarge, resolvedPath)
	}
	if errors.Is(err, os.ErrNotExist) && opts.Explicit {
		return fmt.Errorf("%w %s: %w", ErrConfigNotFound, targetPath, err)
	}
	if _, ok := errors.AsType[*os.PathError](err); ok {
		return fmt.Errorf("%w %s: %w", ErrAccessConfig, resolvedPath, err)
	}
	if isValidationOrDurationError(err) {
		return err
	}
	return fmt.Errorf("%w %s: %w", ErrParseConfig, resolvedPath, err)
}

func isValidationOrDurationError(err error) bool {
	return errors.Is(err, ErrInvalidPresence) ||
		errors.Is(err, ErrInvalidSort) ||
		errors.Is(err, ErrInvalidTimeFormat) ||
		errors.Is(err, ErrNonPositiveTTL) ||
		errors.Is(err, ErrNonPositiveInterval) ||
		errors.Is(err, ErrNegativeGracePeriod) ||
		errors.Is(err, ErrInvalidDuration) ||
		errors.Is(err, ErrMissingUnitSuffix)
}
