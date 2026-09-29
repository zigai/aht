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
	for _, body := range []string{`{}`, `{"schema":2,"harnesses":{}}`, `{"schema":1,"harnesses":{}}`, `{"schema":2,"harnesses":null}`, "invalid-json"} {
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

func TestMissingStateStartsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/artifacts") || r.URL.Query().Get("name") != stateArtifact {
			t.Errorf("unexpected request: %s", r.URL)
		}
		_, _ = fmt.Fprint(w, `{"artifacts":[]}`)
	}))
	t.Cleanup(server.Close)
	state, err := testClient(server).restoreState(t.Context(), "owner/repo", "master", "20")
	if err != nil || !reflect.DeepEqual(state, emptyState()) {
		t.Fatalf("restoreState() = %+v, %v, want empty state", state, err)
	}
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
			if next.Successful[spec.ID] != wantSuccess || regression(spec, next.Harnesses[spec.ID]) != (outcome == "failure") {
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

func TestSupportedOlderSuccessPreservesFailureAboveMaximum(t *testing.T) {
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
	if next.Harnesses[spec.ID] != latest || next.Successful[spec.ID].Version != spec.MaxVersion {
		t.Fatalf("supported success hid upstream failure: %+v", next)
	}
	if regression(spec, next.Harnesses[spec.ID]) {
		t.Fatal("failure above the supported maximum was reported as a regression")
	}
}

func assertRecovery(t *testing.T, state releaseState, id string) {
	t.Helper()
	selected := []candidate{{Harness: id, Version: "1.3.0"}}
	result := hostResult{Harness: id, Version: "1.3.0", Outcome: "success", Revision: "recovery-sha"}
	recovered, _, err := mergeResults(state, selected, []hostResult{result}, "recovery")
	if err != nil || regression(testHarness(t, id), recovered.Harnesses[id]) || recovered.Successful[id].Version != result.Version {
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
