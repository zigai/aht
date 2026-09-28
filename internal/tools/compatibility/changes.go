package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// affectedHosts is conservative for unclassified source paths: new shared code
// must not silently escape compatibility coverage. Adapter families also cover
// their downstream native API consumers even when templates are separate files.
func affectedHosts(paths []string) []string {
	selected := make(map[string]bool)
	for _, path := range paths {
		if unrelatedDocumentation(path) {
			continue
		}
		id := adapterForPath(path)
		if id == "" {
			for _, spec := range defaultCatalog {
				selected[spec.ID] = true
			}
			break
		}
		selected[id] = true
		selectRelatedHosts(selected, id)
	}
	ids := []string{}
	for _, spec := range defaultCatalog {
		if selected[spec.ID] {
			ids = append(ids, spec.ID)
		}
	}
	return ids
}

func unrelatedDocumentation(path string) bool {
	return strings.HasPrefix(path, "docs/") || path == "README.md" || path == "LICENSE" || path == "CHANGELOG.md"
}

func adapterForPath(path string) string {
	rest, ok := strings.CutPrefix(path, "internal/harness/")
	if !ok {
		return ""
	}
	directory, _, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}

	for _, spec := range defaultCatalog {
		if directory == spec.Directory {
			return spec.ID
		}
	}
	return ""
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	data, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(data), nil
}

func commitID(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("%w: comparison requires AHT_COMPAT_BASE and AHT_COMPAT_HEAD", errCompatibility)
	}
	value, err := gitOutput(ctx, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	return strings.TrimSpace(value), err
}

func changedPaths(ctx context.Context, event, base, head string) ([]string, error) {
	if event != "pull_request" && event != "push" {
		return nil, fmt.Errorf("%w: unsupported change event %q", errCompatibility, event)
	}
	headID, err := commitID(ctx, head)
	if err != nil {
		return nil, err
	}
	data, err := changedPathData(ctx, event, base, headID)
	if err != nil {
		return nil, err
	}
	if data == "" {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(data, "\x00"), "\x00"), nil
}

func changedPathData(ctx context.Context, event, base, headID string) (string, error) {
	if event == "push" && len(base) >= 40 && strings.Trim(base, "0") == "" {
		// New branches have no before commit. Every tracked head path is new.
		return gitOutput(ctx, "ls-tree", "-r", "--name-only", "-z", headID)
	}
	baseID, err := commitID(ctx, base)
	if err != nil {
		return "", err
	}
	if event == "pull_request" {
		baseID, err = gitOutput(ctx, "merge-base", baseID, headID)
		if err != nil {
			return "", err
		}
		baseID = strings.TrimSpace(baseID)
	}
	// Disable rename detection so both old and new names are selected. NUL
	// framing preserves whitespace and newlines in deleted/renamed paths.
	return gitOutput(ctx, "diff", "--no-renames", "--name-only", "-z", baseID, headID, "--")
}

func (a application) changes(ctx context.Context) error {
	paths, err := changedPaths(ctx, a.getenv("GITHUB_EVENT_NAME"), a.getenv("AHT_COMPAT_BASE"), a.getenv("AHT_COMPAT_HEAD"))
	if err != nil {
		return err
	}
	plan := releasePlan{Matrix: matrix{Include: []candidate{}}, Observations: []observation{}}
	client := a.releaseClient()
	var state *releaseState
	for _, id := range affectedHosts(paths) {
		spec, err := findHarness(id)
		if err != nil {
			return err
		}
		if spec.Source == "weekly" {
			plan.Matrix.Include = append(plan.Matrix.Include, candidate{Harness: id, Version: ""})
			continue
		}
		if state == nil {
			restored := a.knownGoodState(ctx)
			state = &restored
		}
		// Test changes against the newest release the scheduled workflow proved,
		// so upstream releases cannot turn a change check red on their own.
		version, ok, err := knownGood(spec, *state)
		if err != nil {
			return err
		}
		if ok {
			plan.Matrix.Include = append(plan.Matrix.Include, candidate{Harness: id, Version: version})
			plan.Observations = append(plan.Observations, observation{Harness: id, Version: version, Error: "", Selected: true})
			continue
		}
		// Without proven history, resolve each selected distribution once and
		// pin the host job to the current supported release.
		resolved, err := detect(ctx, emptyState(), id, false, client.supported)
		if err != nil {
			return err
		}
		plan.Matrix.Include = append(plan.Matrix.Include, resolved.Matrix.Include...)
		plan.Observations = append(plan.Observations, resolved.Observations...)
	}
	if err := json.NewEncoder(a.stdout).Encode(plan); err != nil {
		return fmt.Errorf("write change plan: %w", err)
	}
	if planHasErrors(plan) {
		return fmt.Errorf("%w: change selection release sources failed; see observations", errCompatibility)
	}
	if err := a.output("matrix", plan.Matrix); err != nil {
		return err
	}
	return a.output("selected", len(plan.Matrix.Include) > 0)
}

// knownGoodState reads the scheduled workflow's history without writing it. An
// unavailable history only loses pinning, so change checks fall back to the
// current supported releases instead of failing.
func (a application) knownGoodState(ctx context.Context) releaseState {
	state, err := a.restore(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "warning: release state unavailable; using current supported releases: %v\n", err)
		return emptyState()
	}
	return state
}

// knownGood returns the newest successful release for spec, capped at its
// supported maximum.
func knownGood(spec harnessSpec, state releaseState) (string, bool, error) {
	record := state.Successful[spec.ID]
	if record.Source != spec.sourceKey() {
		return "", false, nil
	}
	above, err := aboveMaximum(spec, record.Version)
	if err != nil {
		return "", false, err
	}
	if above {
		return spec.MaxVersion, true, nil
	}
	return record.Version, true, nil
}

func selectRelatedHosts(selected map[string]bool, id string) {
	spec, err := findHarness(id)
	if err != nil || spec.Family == "" {
		return
	}
	for _, related := range defaultCatalog {
		if related.Family == spec.Family {
			selected[related.ID] = true
		}
	}
}
