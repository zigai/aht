package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestRestoreOnlyTrustedDefaultBranchState(t *testing.T) {
	state := emptyState()
	state.Harnesses["droid"] = checkedRelease{Source: testHarness(t, "droid").sourceKey(), Version: "1.2.3", Outcome: "failure", RunURL: "previous-run"}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	archive := stateZip(t, string(body))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github/repos/owner/repo/actions/artifacts":
			if r.URL.Query().Get("name") != stateArtifact {
				t.Error("missing artifact-name filter")
			}
			_, _ = fmt.Fprint(w, `{"artifacts":[
    {"id":6,"expired":true,"workflow_run":{"id":60,"head_branch":"master"}},
    {"id":5,"workflow_run":{"id":50,"head_branch":"feature"}},
    {"id":4,"workflow_run":{"id":40,"head_branch":"master"}},
    {"id":3,"workflow_run":{"id":30,"head_branch":"master"}},
    {"id":2,"workflow_run":{"id":20,"head_branch":"master"}},
    {"id":1,"workflow_run":{"id":10,"head_branch":"master"}}]}`)
		case "/github/repos/owner/repo/actions/runs/30":
			_, _ = fmt.Fprint(w, `{"path":".github/workflows/compatibility-releases.yml","event":"pull_request"}`)
		case "/github/repos/owner/repo/actions/runs/20":
			_, _ = fmt.Fprint(w, `{"path":".github/workflows/ci.yml","event":"workflow_dispatch"}`)
		case "/github/repos/owner/repo/actions/runs/10":
			_, _ = fmt.Fprint(w, `{"path":".github/workflows/compatibility-releases.yml","event":"schedule"}`)
		case "/github/repos/owner/repo/actions/artifacts/1/zip":
			_, _ = w.Write(archive)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	restored, err := testClient(server).restoreState(t.Context(), "owner/repo", "master", "40")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, state) {
		t.Fatalf("restored = %+v", restored)
	}
}

func TestMissingStateAndAPIFailureAreDifferent(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"artifacts":[]}`)
			}))
			t.Cleanup(server.Close)
			state, err := testClient(server).restoreState(t.Context(), "owner/repo", "master", "1")
			if status == http.StatusOK {
				if err != nil || !reflect.DeepEqual(state, emptyState()) {
					t.Fatalf("bootstrap = %+v, %v", state, err)
				}
			} else if !errors.Is(err, errCompatibility) {
				t.Fatalf("API failure silently reset state: %v", err)
			}
		})
	}
}

func TestCorruptStateCannotResetBaseline(t *testing.T) {
	for _, body := range []string{`{}`, `{"schema":2,"harnesses":{}}`, `{"schema":1,"harnesses":null}`, `{"schema":1,"harnesses":{"droid":{"source":"npm:droid","version":"latest","outcome":"success"}}}`, `{"schema":1,"harnesses":{"droid":{"source":"npm:droid","version":"1.2.3","outcome":"incomplete"}}}`, "invalid-json"} {
		if _, err := decodeStateArchive(stateZip(t, body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	if _, err := decodeStateArchive([]byte("invalid-zip")); err == nil {
		t.Fatal("accepted invalid archive")
	}
}

func TestMalformedArtifactListingCannotResetBaseline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)
	_, err := testClient(server).restoreState(t.Context(), "owner/repo", "master", "1")
	if !errors.Is(err, errCompatibility) {
		t.Fatalf("malformed listing silently reset state: %v", err)
	}
}

func TestLegacyStateMigration(t *testing.T) {
	state, err := decodeStateArchive(stateZip(t, `{"schema":1,"harnesses":{
		"droid":{"source":"npm:droid","version":"1.2.3","outcome":"success","run_url":"passed"},
		"codex":{"source":"npm:@openai/codex","version":"0.150.0","outcome":"failure","run_url":"failed"}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	if state.Schema != stateSchema || state.Successful["droid"] != state.Harnesses["droid"] || len(state.Successful) != 1 {
		t.Fatalf("lost successful history: %+v", state)
	}
	previous := state.Harnesses["codex"]
	if previous.Outcome != "incomplete" || previous.RunURL != "failed" {
		t.Fatalf("lost failed observation: %+v", previous)
	}
	check, err := needsCheck(testHarness(t, "codex"), previous.Version, previous, false)
	if err != nil || !check || !unresolvedFailure(state) {
		t.Fatalf("legacy failure must remain visible and retryable: %v, %v", check, err)
	}
}

func TestStateFallbackOnlyWhenV2Absent(t *testing.T) {
	for _, tc := range []struct{ name, current string }{
		{"legacy", ""}, {"corrupt current", "invalid-json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requested []string
			server := migrationServer(t, tc.current, &requested)
			state, err := testClient(server).restoreState(t.Context(), "owner/repo", "master", "20")
			if tc.current != "" {
				if err == nil || !reflect.DeepEqual(requested, []string{stateArtifact}) {
					t.Fatalf("corrupt V2 reset history: %v, %v", requested, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(state, emptyState()) || !reflect.DeepEqual(requested, []string{stateArtifact, "compatibility-release-state-v1"}) {
				t.Fatalf("legacy fallback = %+v, %v, %v", state, requested, err)
			}
		})
	}
}

func migrationServer(t *testing.T, current string, requested *[]string) *httptest.Server {
	t.Helper()
	body := current
	if body == "" {
		body = `{"schema":1,"harnesses":{}}`
	}
	archive := stateZip(t, body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/artifacts"):
			name := r.URL.Query().Get("name")
			*requested = append(*requested, name)
			if name == stateArtifact && current == "" {
				_, _ = fmt.Fprint(w, `{"artifacts":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"artifacts":[{"id":1,"workflow_run":{"id":10,"head_branch":"master"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/runs/10"):
			_, _ = fmt.Fprint(w, `{"path":".github/workflows/compatibility-releases.yml","event":"schedule"}`)
		case strings.HasSuffix(r.URL.Path, "/1/zip"):
			_, _ = w.Write(archive)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAttemptHistoryAndRecovery(t *testing.T) {
	spec := testHarness(t, "droid")
	state := emptyState()
	prior := checkedRelease{Source: spec.sourceKey(), Version: "1.2.3", Outcome: "success", RunURL: "prior", Revision: "prior-sha"}
	state.Harnesses[spec.ID], state.Successful[spec.ID] = prior, prior
	selected := []candidate{{Harness: spec.ID, Version: "1.3.0"}}
	for _, outcome := range []string{"infrastructure", "incomplete", "failure", "success"} {
		t.Run(outcome, func(t *testing.T) {
			result := hostResult{Harness: spec.ID, Version: "1.3.0", Outcome: outcome, Revision: "current-sha"}
			next, incomplete, err := mergeResults(state, selected, []hostResult{result}, "attempt")
			if err != nil {
				t.Fatal(err)
			}
			attempt := next.Harnesses[spec.ID]
			assertAttempt(t, spec, result, attempt, incomplete)
			wantSuccess := prior
			if outcome == "success" {
				wantSuccess = attempt
			}
			if next.Successful[spec.ID] != wantSuccess || unresolvedFailure(next) != (outcome != "success") {
				t.Fatalf("successful history or failure visibility = %+v", next)
			}
			if state.Harnesses[spec.ID] != prior || state.Successful[spec.ID] != prior {
				t.Fatal("mutated restored history")
			}
			assertRecovery(t, next, spec.ID)
		})
	}
}

func assertAttempt(t *testing.T, spec harnessSpec, result hostResult, attempt checkedRelease, incomplete []string) {
	t.Helper()
	if attempt.Outcome != result.Outcome || attempt.Revision != result.Revision || attempt.RunURL != "attempt" {
		t.Fatalf("attempt = %+v", attempt)
	}
	retry := result.Outcome == "infrastructure" || result.Outcome == "incomplete"
	check, err := needsCheck(spec, result.Version, attempt, false)
	if err != nil || check != retry || (len(incomplete) > 0) != retry {
		t.Fatalf("retry = %v, incomplete = %v, error = %v", check, incomplete, err)
	}
}

func TestSupportedOlderSuccessPreservesNewerFailure(t *testing.T) {
	spec := testHarness(t, "kimi-code")
	state := emptyState()
	latest := checkedRelease{Source: spec.sourceKey(), Version: "1.52.0", Outcome: "failure", RunURL: "upstream-run"}
	state.Harnesses[spec.ID] = latest
	selected := []candidate{{Harness: spec.ID, Version: spec.MaxVersion}}
	result := hostResult{Harness: spec.ID, Version: spec.MaxVersion, Outcome: "success", Revision: "supported-sha"}
	next, _, err := mergeResults(state, selected, []hostResult{result}, "supported-run")
	if err != nil {
		t.Fatal(err)
	}
	if next.Harnesses[spec.ID] != latest || next.Successful[spec.ID].Version != spec.MaxVersion || !unresolvedFailure(next) {
		t.Fatalf("supported success hid upstream failure: %+v", next)
	}
}

func assertRecovery(t *testing.T, state releaseState, id string) {
	t.Helper()
	selected := []candidate{{Harness: id, Version: "1.3.0"}}
	result := hostResult{Harness: id, Version: "1.3.0", Outcome: "success", Revision: "recovery-sha"}
	recovered, _, err := mergeResults(state, selected, []hostResult{result}, "recovery")
	if err != nil || unresolvedFailure(recovered) || recovered.Successful[id].Version != result.Version {
		t.Fatalf("recovery = %+v, %v", recovered, err)
	}
	older := []candidate{{Harness: id, Version: "1.1.0"}}
	result.Version = "1.1.0"
	rerun, _, err := mergeResults(recovered, older, []hostResult{result}, "old-rerun")
	if err != nil || !reflect.DeepEqual(rerun, recovered) {
		t.Fatalf("older success rolled back history: %+v, %v", rerun, err)
	}
}

func stateZip(t *testing.T, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("state.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
