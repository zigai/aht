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
			for _, spec := range catalog() {
				selected[spec.ID] = true
			}
			break
		}
		selected[id] = true
		switch id {
		case "pi", "omp":
			selected["pi"], selected["omp"] = true, true
		case "opencode", "kilo":
			selected["opencode"], selected["kilo"] = true, true
		}
	}
	ids := []string{}
	for _, spec := range catalog() {
		if selected[spec.ID] {
			ids = append(ids, spec.ID)
		}
	}
	return ids
}

func unrelatedDocumentation(path string) bool {
	if path == "docs/compatibility.md" {
		return false
	}
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
	if directory == "kimi" {
		return "kimi-code"
	}
	for _, spec := range catalog() {
		if directory == spec.ID {
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
	if event != "pull_request" && event != "push" && event != "merge_group" {
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
	for _, id := range affectedHosts(paths) {
		spec, err := findHarness(id)
		if err != nil {
			return err
		}
		if spec.Source == "weekly" {
			plan.Matrix.Include = append(plan.Matrix.Include, candidate{Harness: id, Version: ""})
			continue
		}
		// Like probe, use an empty state and never restore or publish the hourly
		// cache. Resolve each selected distribution once, then pin the host job.
		resolved, err := detect(ctx, emptyState(), id, false, a.releaseClient().latest)
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
