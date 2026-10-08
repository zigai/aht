//go:build compatibility

package hostcompat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Print mode authenticates even for the native /hooks command. Use agy's
// documented Gemini API-key provider and custom endpoint instead of a Google
// account or the developer's OS keyring:
// https://antigravity.google/docs/cli/install#using-a-gemini-api-key
// /hooks must only inspect local hooks, not make any model requests. The key is
// a fixture for this local endpoint, never a credential for Google's service.
func prepareAgyHookInspection(t *testing.T, host *isolatedHost) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Errorf("discovery-only /hooks unexpectedly called the model provider: %s %s", request.Method, request.URL.Path)
		http.Error(writer, "hook inspection must not call a model", http.StatusForbidden)
	}))
	t.Cleanup(provider.Close)
	host.writeFile(t, filepath.Join(host.home, ".gemini", "antigravity-cli", "settings.json"), `{"modelProvider":"gemini"}`)
	host.env = append(host.env, "GEMINI_API_KEY=aht-local-provider", "GOOGLE_GEMINI_BASE_URL="+provider.URL)
}
