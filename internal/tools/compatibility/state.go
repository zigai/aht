package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
)

func emptyState() releaseState {
	return releaseState{Schema: stateSchema, Harnesses: map[string]checkedRelease{}}
}

func validateState(state releaseState) error {
	if state.Schema != stateSchema || state.Harnesses == nil {
		return fmt.Errorf("%w: invalid state; refusing to reset release history", errCompatibility)
	}
	for _, record := range state.Harnesses {
		if _, err := parseVersion(record.Version); err != nil {
			return err
		}
		if record.Source == "" || !completed(record.Outcome) {
			return fmt.Errorf("%w: invalid result in state", errCompatibility)
		}
	}
	return nil
}

func completed(outcome string) bool { return outcome == "success" || outcome == "failure" }

func decodeStateArchive(body []byte) (releaseState, error) {
	state := emptyState()
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return state, fmt.Errorf("open state archive: %w", err)
	}
	file, err := archive.Open("state.json")
	if err != nil {
		return state, fmt.Errorf("open state.json: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxMetadataBytes+1))
	if err != nil {
		return state, fmt.Errorf("read state.json: %w", err)
	}
	if len(data) > maxMetadataBytes {
		return state, fmt.Errorf("%w: state exceeds size limit", errCompatibility)
	}
	var decoded releaseState
	if err := json.Unmarshal(data, &decoded); err != nil {
		return state, fmt.Errorf("decode state.json: %w", err)
	}
	return decoded, validateState(decoded)
}

func mergeResults(state releaseState, candidates []candidate, results []hostResult, runURL string) (releaseState, []string, error) {
	next := releaseState{Schema: state.Schema, Harnesses: maps.Clone(state.Harnesses)}
	resultMap := make(map[string]hostResult, len(results))
	for _, res := range results {
		if completed(res.Outcome) {
			resultMap[res.Harness+":"+res.Version] = res
		}
	}
	var incomplete []string
	for _, selected := range candidates {
		spec, err := findHarness(selected.Harness)
		if err != nil {
			return next, incomplete, err
		}
		result, ok := resultMap[selected.Harness+":"+selected.Version]
		if !ok {
			incomplete = append(incomplete, selected.Harness)
			continue
		}
		newer, err := alreadyNewer(next.Harnesses[spec.ID], spec, result.Version)
		if err != nil {
			return next, incomplete, err
		}
		// A manual rerun of an old workflow must not roll shared state backwards.
		if newer {
			continue
		}
		next.Harnesses[spec.ID] = checkedRelease{Source: spec.sourceKey(), Version: result.Version, Outcome: result.Outcome, RunURL: runURL}
	}
	return next, incomplete, nil
}

func alreadyNewer(previous checkedRelease, spec harnessSpec, version string) (bool, error) {
	current, err := parseVersion(version)
	if err != nil {
		return false, err
	}
	if previous.Source != spec.sourceKey() {
		return false, nil
	}
	before, err := parseVersion(previous.Version)
	if err != nil {
		return false, err
	}
	return slices.Compare(before[:], current[:]) > 0, nil
}
