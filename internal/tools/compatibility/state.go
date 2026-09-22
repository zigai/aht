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
	return releaseState{Schema: stateSchema, Harnesses: map[string]checkedRelease{}, Successful: map[string]checkedRelease{}}
}

func validateState(state releaseState) error {
	if state.Schema != stateSchema || state.Harnesses == nil || state.Successful == nil {
		return fmt.Errorf("%w: invalid state; refusing to reset release history", errCompatibility)
	}
	for _, record := range state.Harnesses {
		if _, err := parseVersion(record.Version); err != nil {
			return err
		}
		if record.Source == "" || !recordedOutcome(record.Outcome) {
			return fmt.Errorf("%w: invalid result in state", errCompatibility)
		}
	}
	for _, record := range state.Successful {
		if _, err := parseVersion(record.Version); err != nil {
			return err
		}
		if record.Source == "" || record.Outcome != "success" {
			return fmt.Errorf("%w: invalid successful release", errCompatibility)
		}
	}
	return nil
}

func recordedOutcome(outcome string) bool {
	return outcome == "success" || outcome == "failure" || outcome == "infrastructure" || outcome == "incomplete"
}

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
	if decoded.Schema == 1 {
		// V1 did not distinguish install failures or retain earlier successes.
		// Preserve all observations; only successful records can seed that history.
		decoded.Schema = stateSchema
		decoded.Successful = map[string]checkedRelease{}
		for id, record := range decoded.Harnesses {
			if record.Outcome != "success" && record.Outcome != "failure" {
				return state, fmt.Errorf("%w: invalid legacy result", errCompatibility)
			}
			if record.Outcome == "success" {
				decoded.Successful[id] = record
			} else {
				// A legacy failure may be an installation failure. Retry once
				// under V2 to classify it without erasing the failed observation.
				record.Outcome = "incomplete"
				decoded.Harnesses[id] = record
			}
		}
	}
	return decoded, validateState(decoded)
}

func mergeResults(state releaseState, candidates []candidate, results []hostResult, runURL string) (releaseState, []string, error) {
	next := releaseState{Schema: state.Schema, Harnesses: maps.Clone(state.Harnesses), Successful: maps.Clone(state.Successful)}
	resultMap := make(map[string]hostResult, len(results))
	for _, res := range results {
		if recordedOutcome(res.Outcome) {
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
			result = hostResult{Harness: selected.Harness, Version: selected.Version, Outcome: "incomplete", Revision: ""}
		}
		if result.Outcome == "incomplete" || result.Outcome == "infrastructure" {
			incomplete = append(incomplete, selected.Harness)
		}
		record := checkedRelease{Source: spec.sourceKey(), Version: result.Version, Outcome: result.Outcome, RunURL: runURL, Revision: result.Revision}
		if result.Outcome == "success" {
			if err := recordLatest(next.Successful, spec, record); err != nil {
				return next, incomplete, err
			}
		}
		if err := recordLatest(next.Harnesses, spec, record); err != nil {
			return next, incomplete, err
		}
	}
	return next, incomplete, nil
}

func recordLatest(records map[string]checkedRelease, spec harnessSpec, record checkedRelease) error {
	newer, err := alreadyNewer(records[spec.ID], spec, record.Version)
	if err != nil {
		return err
	}
	// A manual rerun of an old workflow must not roll either history backwards.
	if !newer {
		records[spec.ID] = record
	}
	return nil
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
