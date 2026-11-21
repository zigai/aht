package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestReleaseSources(t *testing.T) {
	tests := []struct {
		id   string
		path string
		body string
		want string
		bad  bool
	}{
		{
			"claude",
			"/npm/@anthropic-ai%2Fclaude-code/latest",
			`{"name":"@anthropic-ai/claude-code","version":"1.2.3"}`,
			"1.2.3",
			false,
		},
		{
			"hermes",
			"/pypi/hermes-agent/json",
			`{"info":{"name":"hermes-agent","version":"1.2.3"},"urls":[{"yanked":false}]}`,
			"1.2.3",
			false,
		},
		{
			"kimi-code",
			"/npm/@moonshot-ai%2Fkimi-code/latest",
			`{"name":"@moonshot-ai/kimi-code","version":"2.1.1"}`,
			"2.1.1",
			false,
		},
		{
			"goose",
			"/github/repos/aaif-goose/goose/releases/latest",
			`{"tag_name":"v1.2.3","assets":[{"name":"download_cli.sh","state":"uploaded"}]}`,
			"v1.2.3",
			false,
		},
		{"grok", "/channel", "1.2.3\n", "1.2.3", false},
		{"droid", "/npm/droid/latest", `{"name":"different","version":"1.2.3"}`, "", true},
		{
			"hermes",
			"/pypi/hermes-agent/json",
			`{"info":{"name":"hermes-agent","version":"1.2.3"},"urls":[{"yanked":true}]}`,
			"",
			true,
		},
		{
			"goose",
			"/github/repos/aaif-goose/goose/releases/latest",
			`{"tag_name":"v1.2.3","assets":[]}`,
			"",
			true,
		},
		{
			"goose",
			"/github/repos/aaif-goose/goose/releases/latest",
			`{"prerelease":true}`,
			"",
			true,
		},
		{"goose", "/github/repos/aaif-goose/goose/releases/latest", `{"draft":true}`, "", true},
		{"grok", "/channel", "<html>unavailable</html>", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.id+tt.body, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				}),
			)
			t.Cleanup(server.Close)
			client := testClient(server)

			spec := testHarness(t, tt.id)
			if spec.Source == harness.DistributionChannel {
				spec.URL = server.URL + "/channel"
			}

			version, err := client.latest(t.Context(), spec)
			if (err != nil) != tt.bad || version != tt.want {
				t.Fatalf("latest = %q, %v", version, err)
			}
		})
	}
}

func TestSupportedCapDoesNotHideLatestRelease(t *testing.T) {
	for _, tc := range []struct {
		latest    string
		supported string
	}{
		{"2.0.0", "2.0.0"}, {"2.1.0", "2.1.0"}, {"2.1.1", "2.1.0"},
	} {
		t.Run(tc.latest, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(
						w,
						`{"name":"@moonshot-ai/kimi-code","version":%q}`,
						tc.latest,
					)
				}),
			)
			t.Cleanup(server.Close)
			client := testClient(server)
			spec := testHarness(t, "kimi-code")
			spec.MaxVersion = "2.1.0"

			version, err := client.supported(t.Context(), spec)
			if err != nil || version != tc.supported {
				t.Fatalf("supported = %q, %v", version, err)
			}

			plan, err := detect(
				t.Context(),
				emptyState(),
				spec.ID,
				false,
				everyHarness,
				client.latest,
			)
			if err != nil || len(plan.Matrix.Include) != 1 ||
				plan.Matrix.Include[0].Version != tc.latest {
				t.Fatalf("upstream detection = %+v, %v", plan, err)
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

func testClient(server *httptest.Server) *releaseClient {
	client := newReleaseClient(server.URL+"/github", "fixture-token")
	client.github.RetryDelay = 0
	client.npmBase = server.URL + "/npm"
	client.pypiBase = server.URL + "/pypi"

	return client
}

func TestNPMDependencyDateCoversExactlyPinnedDependencies(t *testing.T) {
	const release = `{"name":"@openai/codex","dependencies":{"ranged":"^1.0.0"},` +
		`"optionalDependencies":{"@openai/codex-linux-x64":"npm:@openai/codex@1.2.3-linux-x64",` +
		`"@openai/codex-darwin-x64":"npm:@openai/codex@1.2.3-darwin-x64","platform-helper":"4.5.6"}}`

	tests := []struct {
		name     string
		manifest string
		packages map[string]string
		want     string
		bad      bool
	}{
		{
			name:     "latest exact pin wins",
			manifest: release,
			packages: map[string]string{
				"@openai%2Fcodex": `{"versions":{"1.2.3":{"name":"@openai/codex"}},"time":{` +
					`"1.2.3":"2026-01-02T03:00:00.000Z",` +
					`"1.2.3-linux-x64":"2026-01-02T03:05:00.000Z",` +
					`"1.2.3-darwin-x64":"2026-01-02T03:02:00.000Z"}}`,
				"platform-helper": `{"time":{"4.5.6":"2026-01-02T03:04:00.000Z"}}`,
			},
			want: "2026-01-02T03:05:00Z",
		},
		{
			name:     "release without publish time",
			manifest: `{"name":"@openai/codex"}`,
			packages: map[string]string{
				"@openai%2Fcodex": `{"time":{"1.2.2":"2026-01-02T03:00:00.000Z"}}`,
			},
			bad: true,
		},
		{
			name:     "exact pin without publish time",
			manifest: `{"name":"@openai/codex","optionalDependencies":{"platform-helper":"4.5.6"}}`,
			packages: map[string]string{
				"@openai%2Fcodex": `{"time":{"1.2.3":"2026-01-02T03:00:00.000Z"}}`,
				"platform-helper": `{"time":{}}`,
			},
			bad: true,
		},
		{
			name:     "different package",
			manifest: `{"name":"other"}`,
			packages: map[string]string{
				"@openai%2Fcodex": `{"time":{"1.2.3":"2026-01-02T03:00:00.000Z"}}`,
			},
			bad: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := strings.TrimPrefix(r.URL.EscapedPath(), "/npm/")
					if path == "@openai%2Fcodex/1.2.3" {
						_, _ = fmt.Fprint(w, tt.manifest)
						return
					}

					body, ok := tt.packages[path]
					if !ok {
						http.NotFound(w, r)
						return
					}

					_, _ = fmt.Fprint(w, body)
				}),
			)
			t.Cleanup(server.Close)

			date, err := testClient(
				server,
			).npmDependencyDate(t.Context(), testHarness(t, "codex"), "1.2.3")
			if tt.bad {
				if err == nil {
					t.Fatalf("date = %s, want an error", date)
				}

				return
			}

			if err != nil || date.UTC().Format(time.RFC3339) != tt.want {
				t.Fatalf("date = %s, %v; want %s", date, err, tt.want)
			}
		})
	}
}
