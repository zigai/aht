package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const (
	versionComponents = 3
	seriesComponents  = 2
	stateSchema       = 1
)

var (
	errCompatibility       = errors.New("compatibility")
	stableVersionPattern   = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	observedVersionPattern = regexp.MustCompile(`v?[0-9]+\.[0-9]+\.[0-9]+`)

	defaultCatalog = []harnessSpec{
		{ID: "claude", Source: "npm", Package: "@anthropic-ai/claude-code", Repo: "", Asset: "", URL: ""},
		{ID: "codex", Source: "npm", Package: "@openai/codex", Repo: "", Asset: "", URL: ""},
		{ID: "cursor", Source: "weekly", Package: "", Repo: "", Asset: "", URL: ""},
		{ID: "copilot", Source: "npm", Package: "@github/copilot", Repo: "", Asset: "", URL: ""},
		{ID: "cline", Source: "npm", Package: "cline", Repo: "", Asset: "", URL: ""},
		{ID: "kimi-code", Source: "pypi", Package: "kimi-cli", Repo: "", Asset: "", URL: ""},
		{ID: "grok", Source: "channel", Package: "", Repo: "", Asset: "", URL: "https://x.ai/cli/stable"},
		{ID: "goose", Source: "github", Package: "", Repo: "aaif-goose/goose", Asset: "download_cli.sh", URL: ""},
		{ID: "pi", Source: "npm", Package: "@earendil-works/pi-coding-agent", Repo: "", Asset: "", URL: ""},
		{ID: "omp", Source: "npm", Package: "@oh-my-pi/pi-coding-agent", Repo: "", Asset: "", URL: ""},
		{ID: "opencode", Source: "npm", Package: "opencode-ai", Repo: "", Asset: "", URL: ""},
		{ID: "agy", Source: "github", Package: "", Repo: "google-antigravity/antigravity-cli", Asset: "agy_cli_linux_x64.tar.gz", URL: ""},
		{ID: "kilo", Source: "npm", Package: "@kilocode/cli", Repo: "", Asset: "", URL: ""},
		{ID: "droid", Source: "npm", Package: "droid", Repo: "", Asset: "", URL: ""},
		{ID: "openclaw", Source: "npm", Package: "openclaw", Repo: "", Asset: "", URL: ""},
		{ID: "hermes", Source: "pypi", Package: "hermes-agent", Repo: "", Asset: "", URL: ""},
	}
)

type harnessSpec struct {
	ID      string
	Source  string
	Package string
	Repo    string
	Asset   string
	URL     string
}

type candidate struct {
	Harness string `json:"harness"`
	Version string `json:"version,omitempty"`
}

type matrix struct {
	Include []candidate `json:"include"`
}

type observation struct {
	Harness  string `json:"harness"`
	Version  string `json:"version,omitempty"`
	Error    string `json:"error,omitempty"`
	Selected bool   `json:"selected"`
}

type releasePlan struct {
	Matrix       matrix        `json:"matrix"`
	Observations []observation `json:"observations"`
}

type checkedRelease struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Outcome string `json:"outcome"`
	RunURL  string `json:"run_url"`
}

type releaseState struct {
	Schema    int                       `json:"schema"`
	Harnesses map[string]checkedRelease `json:"harnesses"`
}

type hostResult struct {
	Harness string `json:"harness"`
	Version string `json:"version"`
	Outcome string `json:"outcome"`
}

func catalog() []harnessSpec {
	// Public release and installation contracts are documented in docs/compatibility.md.
	return defaultCatalog
}

func findHarness(id string) (harnessSpec, error) {
	for _, spec := range defaultCatalog {
		if spec.ID == id {
			return spec, nil
		}
	}
	return harnessSpec{}, fmt.Errorf("%w: unknown harness %q", errCompatibility, id)
}

func (h harnessSpec) sourceKey() string {
	target := h.Package
	if target == "" {
		target = h.Repo
	}
	if target == "" {
		target = h.URL
	}
	if target == "" {
		target = h.ID
	}
	if target != "" {
		return h.Source + ":" + target
	}
	return h.Source
}

func parseVersion(value string) ([versionComponents]uint64, error) {
	var parts [versionComponents]uint64
	match := stableVersionPattern.FindStringSubmatch(value)
	if len(match) != versionComponents+1 {
		return parts, fmt.Errorf("%w: expected a stable X.Y.Z version, got %q", errCompatibility, value)
	}
	for i := range versionComponents {
		number, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return parts, fmt.Errorf("version component: %w", err)
		}
		parts[i] = number
	}
	return parts, nil
}

func needsCheck(spec harnessSpec, version string, previous checkedRelease, force bool) (bool, error) {
	current, err := parseVersion(version)
	if err != nil {
		return false, err
	}
	if force || previous.Source != spec.sourceKey() {
		return true, nil
	}
	before, err := parseVersion(previous.Version)
	if err != nil {
		return false, err
	}
	return slices.Compare(current[:seriesComponents], before[:seriesComponents]) > 0, nil
}

func detect(ctx context.Context, state releaseState, selection string, force bool, latest func(context.Context, harnessSpec) (string, error)) (releasePlan, error) {
	plan := releasePlan{Matrix: matrix{Include: []candidate{}}, Observations: []observation{}}
	if selection != "all" {
		spec, err := findHarness(selection)
		if err != nil {
			return plan, err
		}
		if spec.Source == "weekly" {
			return plan, fmt.Errorf("%w: %s uses the weekly workflow", errCompatibility, selection)
		}
	}
	var entries []harnessSpec
	for _, spec := range catalog() {
		if spec.Source != "weekly" && (selection == "all" || selection == spec.ID) {
			entries = append(entries, spec)
		}
	}
	plan.Observations = make([]observation, len(entries))
	// Concurrency is bounded by the fixed catalog; every request has a timeout.
	var group sync.WaitGroup
	for i, spec := range entries {
		group.Go(func() { plan.Observations[i] = observe(ctx, spec, state.Harnesses[spec.ID], force, latest) })
	}
	group.Wait()
	if err := ctx.Err(); err != nil {
		return plan, fmt.Errorf("release detection: %w", err)
	}
	for _, item := range plan.Observations {
		if item.Selected {
			plan.Matrix.Include = append(plan.Matrix.Include, candidate{Harness: item.Harness, Version: item.Version})
		}
	}
	return plan, nil
}

func observe(ctx context.Context, spec harnessSpec, previous checkedRelease, force bool, latest func(context.Context, harnessSpec) (string, error)) observation {
	item := observation{Harness: spec.ID, Version: "", Error: "", Selected: false}
	version, err := latest(ctx, spec)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	item.Version = version
	item.Selected, err = needsCheck(spec, version, previous, force)
	if err != nil {
		item.Error = err.Error()
	}
	return item
}

func checkedVersion(log, id, version string) bool {
	expected := strings.TrimPrefix(version, "v")
	for line := range strings.SplitSeq(log, "\n") {
		_, value, found := strings.Cut(line, "current "+id+": ")
		if !found {
			continue
		}
		for _, location := range observedVersionPattern.FindAllStringIndex(value, -1) {
			start, end := location[0], location[1]
			if start > 0 && versionCharacter(value[start-1]) {
				continue
			}
			// Copilot ends its version sentence with a period. Allow that
			// punctuation, while rejecting extra version components or suffixes.
			suffix := strings.TrimPrefix(value[end:], ".")
			if len(suffix) > 0 && versionCharacter(suffix[0]) {
				continue
			}
			if strings.TrimPrefix(value[start:end], "v") == expected {
				return true
			}
		}
	}
	return false
}

func versionCharacter(char byte) bool {
	return char >= '0' && char <= '9' ||
		char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char == '.' || char == '_' || char == '+' || char == '-'
}
