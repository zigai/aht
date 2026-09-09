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
