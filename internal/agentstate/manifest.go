package agentstate

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/aht/internal/config"
	"github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

const (
	manifestSchemaVersion = 1
	maxManifestBytes      = 1 << 20
	MaxScreenBytes        = 2 << 20
)

var (
	errManifestInvalid         = errors.New("invalid detection manifest")
	errInvalidRegion           = errors.New("invalid manifest region")
	errManifestTooLarge        = errors.New("detection manifest exceeds 1 MiB")
	ErrScreenTooLarge          = errors.New("screen fixture exceeds 2 MiB")
	errScreenPathRequired      = errors.New("screen fixture path is required")
	errStdinUnavailable        = errors.New("stdin is unavailable")
	errBundledManifestNotFound = errors.New("bundled detection manifest not found")
	manifestCache              sync.Map
)

type Manifest struct {
	Version int    `json:"version"           toml:"version"`
	Agent   string `json:"agent"             toml:"agent"`
	Rules   []Rule `json:"rules"             toml:"rules"`
	Source  string `json:"source"            toml:"-"`
	Warning string `json:"warning,omitempty" toml:"-"`
}

type Rule struct {
	ID            string   `json:"id"                        toml:"id"`
	State         string   `json:"state"                     toml:"state"`
	Priority      int      `json:"priority"                  toml:"priority"`
	Region        string   `json:"region"                    toml:"region"`
	CaseSensitive bool     `json:"case_sensitive"            toml:"case_sensitive"`
	All           []string `json:"all,omitempty"             toml:"all"`
	Any           []string `json:"any,omitempty"             toml:"any"`
	None          []string `json:"none,omitempty"            toml:"none"`
	RegexAll      []string `json:"regex_all,omitempty"       toml:"regex_all"`
	RegexAny      []string `json:"regex_any,omitempty"       toml:"regex_any"`
	RegexNone     []string `json:"regex_none,omitempty"      toml:"regex_none"`
	TitleAny      []string `json:"title_any,omitempty"       toml:"title_any"`
	TitleRegexAny []string `json:"title_regex_any,omitempty" toml:"title_regex_any"`

	regexAllCompiled      []*regexp.Regexp
	regexAnyCompiled      []*regexp.Regexp
	regexNoneCompiled     []*regexp.Regexp
	titleRegexAnyCompiled []*regexp.Regexp
}

type Loader struct{ ConfigDir string }

type manifestCacheEntry struct {
	fingerprint string
	manifest    Manifest
	err         error
}

func DefaultConfigDir() string {
	configDir := config.UserConfigDir()
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "aht", "detection")
}

func (l Loader) Supports(harness registry.Harness) bool {
	if harnesscatalog.SupportsScreen(harness) {
		return true
	}
	_, err := os.Stat(l.overridePath(harness))
	return err == nil
}

func (l Loader) Load(harness registry.Harness) (Manifest, error) {
	path := l.overridePath(harness)
	key := string(harness) + "\x00" + path
	fingerprint := manifestFileFingerprint(path)
	if cached, ok := manifestCache.Load(key); ok {
		if entry, valid := cached.(manifestCacheEntry); valid &&
			entry.fingerprint == fingerprint {
			return entry.manifest, entry.err
		}
	}

	manifest, err := loadUncached(harness, path)
	manifestCache.Store(key, manifestCacheEntry{
		fingerprint: fingerprint,
		manifest:    manifest,
		err:         err,
	})

	return manifest, err
}

func (l Loader) overridePath(harness registry.Harness) string {
	configDir := l.ConfigDir
	if configDir == "" {
		configDir = DefaultConfigDir()
	}
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, string(harness)+".toml")
}

func ParseManifest(data []byte, harness registry.Harness) (Manifest, error) {
	if len(data) > maxManifestBytes {
		return Manifest{}, errManifestTooLarge
	}
	var manifest Manifest
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: parsing TOML: %w", errManifestInvalid, err)
	}
	if manifest.Agent != string(harness) {
		return Manifest{}, fmt.Errorf("%w: agent %q does not match %q", errManifestInvalid, manifest.Agent, harness)
	}
	if err := manifest.validate(); err != nil {
		return Manifest{}, err
	}
	if err := manifest.compileRules(); err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// LoadExplicitManifest loads, bounds, and parses an explicit manifest file from path for harness.
// Unlike ambient override loading, any read or parse failure returns an error without fallback.
func LoadExplicitManifest(path string, harness registry.Harness) (Manifest, error) {
	data, err := readManifestFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading detection manifest: %w", err)
	}
	manifest, err := ParseManifest(data, harness)
	if err != nil {
		return Manifest{}, fmt.Errorf("parsing detection manifest: %w", err)
	}
	manifest.Source = path
	return manifest, nil
}

// ReadScreenInput reads screen fixture content from path (or stdin if path is "-") bounded by MaxScreenBytes.
func ReadScreenInput(source string, stdin io.Reader) (string, error) {
	if source == "" {
		return "", errScreenPathRequired
	}
	var reader io.Reader
	if source == "-" {
		if stdin == nil {
			return "", errStdinUnavailable
		}
		reader = stdin
	} else {
		file, err := os.Open(source)
		if err != nil {
			return "", fmt.Errorf("open screen fixture: %w", err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxScreenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read screen fixture: %w", err)
	}
	if len(data) > MaxScreenBytes {
		return "", ErrScreenTooLarge
	}
	return string(data), nil
}

func loadUncached(harness registry.Harness, path string) (Manifest, error) {
	var bundled Manifest
	if harnesscatalog.SupportsScreen(harness) {
		var err error
		bundled, err = loadBundled(harness)
		if err != nil {
			return Manifest{}, err
		}
	}
	if path == "" {
		if harnesscatalog.SupportsScreen(harness) {
			return bundled, nil
		}
		return Manifest{}, fmt.Errorf("%w: unsupported screen harness %q", errManifestInvalid, harness)
	}

	data, readErr := readManifestFile(path)
	if errors.Is(readErr, os.ErrNotExist) {
		if harnesscatalog.SupportsScreen(harness) {
			return bundled, nil
		}
		return Manifest{}, fmt.Errorf("%w: unsupported screen harness %q", errManifestInvalid, harness)
	}
	if readErr != nil {
		if !harnesscatalog.SupportsScreen(harness) {
			return Manifest{}, fmt.Errorf("reading local override %s: %w", path, readErr)
		}
		bundled.Warning = fmt.Sprintf("reading local override %s: %v", path, readErr)
		return bundled, nil
	}

	local, parseErr := ParseManifest(data, harness)
	if parseErr != nil {
		if !harnesscatalog.SupportsScreen(harness) {
			return Manifest{}, fmt.Errorf("loading local override %s: %w", path, parseErr)
		}
		bundled.Warning = fmt.Sprintf("ignoring invalid local override %s: %v", path, parseErr)
		return bundled, nil
	}
	local.Source = path

	return local, nil
}

func manifestFileFingerprint(path string) string {
	if path == "" {
		return "none"
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing"
		}
		return "error:" + err.Error()
	}

	return fmt.Sprintf("%d:%d:%d", info.ModTime().UnixNano(), info.Size(), info.Mode())
}

func readManifestFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, errManifestTooLarge
	}
	return data, nil
}

func (manifest *Manifest) compileRules() error {
	for index := range manifest.Rules {
		rule := &manifest.Rules[index]
		var err error
		if rule.regexAllCompiled, err = compileRuleExpressions(rule.RegexAll, rule.CaseSensitive); err != nil {
			return fmt.Errorf("%w: compiling rule %q regex_all: %w", errManifestInvalid, rule.ID, err)
		}
		if rule.regexAnyCompiled, err = compileRuleExpressions(rule.RegexAny, rule.CaseSensitive); err != nil {
			return fmt.Errorf("%w: compiling rule %q regex_any: %w", errManifestInvalid, rule.ID, err)
		}
		if rule.regexNoneCompiled, err = compileRuleExpressions(rule.RegexNone, rule.CaseSensitive); err != nil {
			return fmt.Errorf("%w: compiling rule %q regex_none: %w", errManifestInvalid, rule.ID, err)
		}
		if rule.titleRegexAnyCompiled, err = compileRuleExpressions(rule.TitleRegexAny, rule.CaseSensitive); err != nil {
			return fmt.Errorf("%w: compiling rule %q title_regex_any: %w", errManifestInvalid, rule.ID, err)
		}
	}

	return nil
}

func compileRuleExpressions(expressions []string, caseSensitive bool) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(expressions))
	for _, expression := range expressions {
		pattern, err := regexp.Compile(ruleRegexExpression(expression, caseSensitive))
		if err != nil {
			return nil, fmt.Errorf("compiling regular expression %q: %w", expression, err)
		}
		compiled = append(compiled, pattern)
	}

	return compiled, nil
}

func loadBundled(harnessID registry.Harness) (Manifest, error) {
	adapter, ok := harnesscatalog.Find(harnessID)
	if !ok {
		return Manifest{}, fmt.Errorf("reading bundled manifest for %s: %w", harnessID, errBundledManifestNotFound)
	}
	provider, ok := adapter.(harness.ScreenManifestProvider)
	if !ok {
		return Manifest{}, fmt.Errorf("reading bundled manifest for %s: %w", harnessID, errBundledManifestNotFound)
	}
	manifest, err := ParseManifest([]byte(provider.ScreenManifest()), harnessID)
	if err != nil {
		return Manifest{}, fmt.Errorf("loading bundled manifest for %s: %w", harnessID, err)
	}
	manifest.Source = "bundled:" + string(harnessID)
	return manifest, nil
}

func (manifest *Manifest) validate() error {
	if manifest.Version != manifestSchemaVersion || len(manifest.Rules) == 0 {
		return fmt.Errorf("%w: schema version %d or empty rules", errManifestInvalid, manifest.Version)
	}
	seen := make(map[string]struct{}, len(manifest.Rules))
	for index, rule := range manifest.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("%w: rules[%d] has no id", errManifestInvalid, index)
		}
		if _, exists := seen[rule.ID]; exists {
			return fmt.Errorf("%w: duplicate rule id %q", errManifestInvalid, rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if err := rule.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (rule Rule) validate() error {
	activity, err := registry.NormalizeActivity(rule.State)
	if err != nil || activity == registry.ActivityUnknown || activity == "" {
		return fmt.Errorf("%w: rule %q has unsupported state %q", errManifestInvalid, rule.ID, rule.State)
	}
	if _, err := selectRegion(rule.Region, nil); err != nil {
		return fmt.Errorf("%w: rule %q: %w", errManifestInvalid, rule.ID, err)
	}
	if !rule.hasPositiveMatcher() {
		return fmt.Errorf("%w: rule %q has no positive matcher", errManifestInvalid, rule.ID)
	}
	return rule.validateMatchers()
}

func (rule Rule) hasPositiveMatcher() bool {
	return len(rule.All)+len(rule.Any)+len(rule.RegexAll)+len(rule.RegexAny)+len(rule.TitleAny)+len(rule.TitleRegexAny) > 0
}

func (rule Rule) validateMatchers() error {
	groups := []struct {
		name   string
		values []string
	}{
		{name: "all", values: rule.All},
		{name: "any", values: rule.Any},
		{name: "none", values: rule.None},
		{name: "regex_all", values: rule.RegexAll},
		{name: "regex_any", values: rule.RegexAny},
		{name: "regex_none", values: rule.RegexNone},
		{name: "title_any", values: rule.TitleAny},
		{name: "title_regex_any", values: rule.TitleRegexAny},
	}
	for _, group := range groups {
		for index, value := range group.values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%w: rule %q %s[%d] is empty", errManifestInvalid, rule.ID, group.name, index)
			}
		}
	}
	return nil
}

func sortedRules(rules []Rule) []Rule {
	result := append([]Rule(nil), rules...)
	slices.SortStableFunc(result, func(left, right Rule) int { return cmp.Compare(right.Priority, left.Priority) })
	return result
}

func selectRegion(value string, lines []string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return lines, nil
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 || (parts[0] != "bottom" && parts[0] != "top") {
		return nil, fmt.Errorf("%w: %q", errInvalidRegion, value)
	}
	count, err := strconv.Atoi(parts[1])
	if err != nil || count <= 0 {
		return nil, fmt.Errorf("%w: %q", errInvalidRegion, value)
	}
	if parts[0] == "top" {
		count = min(count, len(lines))
		return lines[:count], nil
	}

	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	count = min(count, len(lines))

	return lines[len(lines)-count:], nil
}
