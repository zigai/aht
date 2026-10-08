//go:build compatibility

package hostcompat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

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
