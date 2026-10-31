//go:build compatibility

package hostcompat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func discoveryEnvironment(t *testing.T, contract hostContract) []string {
	t.Helper()

	if contract.ID != registry.Harness("agy") {
		return nil
	}

	provider := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			t.Errorf(
				"discovery-only /hooks unexpectedly called the model provider: %s %s",
				request.Method,
				request.URL.Path,
			)
			http.Error(writer, "hook inspection must not call a model", http.StatusForbidden)
		}),
	)
	t.Cleanup(provider.Close)

	return []string{"GEMINI_API_KEY=aht-local-provider", "GOOGLE_GEMINI_BASE_URL=" + provider.URL}
}

func prepareAgyHookInspection(t *testing.T, host *isolatedHost) {
	t.Helper()
	host.writeFile(
		t,
		filepath.Join(host.launched.Home, ".gemini", "antigravity-cli", "settings.json"),
		`{"modelProvider":"gemini"}`,
	)
}
