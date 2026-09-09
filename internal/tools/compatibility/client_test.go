package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseSources(t *testing.T) {
	tests := []struct {
		id, path, body, want string
		bad                  bool
	}{
		{"claude", "/npm/@anthropic-ai%2Fclaude-code/latest", `{"name":"@anthropic-ai/claude-code","version":"1.2.3"}`, "1.2.3", false},
		{"hermes", "/pypi/hermes-agent/json", `{"info":{"name":"hermes-agent","version":"1.2.3"},"urls":[{"yanked":false}]}`, "1.2.3", false},
		{"goose", "/github/repos/aaif-goose/goose/releases/latest", `{"tag_name":"v1.2.3","assets":[{"name":"download_cli.sh","state":"uploaded"}]}`, "v1.2.3", false},
		{"grok", "/channel", "1.2.3\n", "1.2.3", false},
		{"droid", "/npm/droid/latest", `{"name":"different","version":"1.2.3"}`, "", true},
		{"hermes", "/pypi/hermes-agent/json", `{"info":{"name":"hermes-agent","version":"1.2.3"},"urls":[{"yanked":true}]}`, "", true},
		{"goose", "/github/repos/aaif-goose/goose/releases/latest", `{"tag_name":"v1.2.3","assets":[]}`, "", true},
		{"goose", "/github/repos/aaif-goose/goose/releases/latest", `{"prerelease":true}`, "", true},
		{"goose", "/github/repos/aaif-goose/goose/releases/latest", `{"draft":true}`, "", true},
		{"grok", "/channel", "<html>unavailable</html>", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.id+tt.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != tt.path {
					t.Errorf("path = %s, want %s", r.URL.EscapedPath(), tt.path)
				}
				wantAuth := ""
				if strings.HasPrefix(tt.path, "/github/") {
					wantAuth = "Bearer fixture-token"
				}
				if r.Header.Get("Authorization") != wantAuth {
					t.Errorf("unexpected authorization header for %s", tt.path)
				}
				_, _ = fmt.Fprint(w, tt.body)
			}))
			t.Cleanup(server.Close)
			client := testClient(server)
			spec := testHarness(t, tt.id)
			if spec.Source == "channel" {
				spec.URL = server.URL + "/channel"
			}
			version, err := client.latest(t.Context(), spec)
			if (err != nil) != tt.bad || version != tt.want {
				t.Fatalf("latest = %q, %v", version, err)
			}
		})
	}
}

func TestHTTPFailuresAndLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/large":
			_, _ = fmt.Fprint(w, strings.Repeat("x", maxMetadataBytes+1))
		case "/json":
			_, _ = fmt.Fprint(w, "invalid-json")
		}
	}))
	t.Cleanup(server.Close)
	client := testClient(server)
	for _, path := range []string{"/status", "/large"} {
		if _, err := client.get(t.Context(), server.URL+path); !errors.Is(err, errCompatibility) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	var data map[string]string
	if err := client.getJSON(t.Context(), server.URL+"/json", &data); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.get(ctx, server.URL+"/status"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestRedirectDoesNotForwardToken(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("forwarded token to artifact storage")
		}
		_, _ = fmt.Fprint(w, "state")
	}))
	t.Cleanup(destination.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("missing API token")
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	client := testClient(origin)
	body, err := client.get(t.Context(), origin.URL+"/github/artifact")
	if err != nil || string(body) != "state" {
		t.Fatalf("redirect = %q, %v", body, err)
	}
}

func testClient(server *httptest.Server) *releaseClient {
	client := newReleaseClient("fixture-token")
	client.http = server.Client()
	client.http.CheckRedirect = checkRedirect
	client.githubBase = server.URL + "/github"
	client.npmBase = server.URL + "/npm"
	client.pypiBase = server.URL + "/pypi"
	return client
}
