//go:build compatibility

package hostcompat

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

// The native abort endpoint ends the active turn, not the durable session.
// https://opencode.ai/docs/server/#sessions
// https://github.com/Kilo-Org/kilocode/blob/main/packages/sdk/js/src/v2/gen/types.gen.ts
func (host *isolatedHost) runServerInterruption(t *testing.T, env []string) {
	t.Helper()
	baseURL, client, process := startNativeServer(t, host, env)
	query := "?directory=" + url.QueryEscape(host.launched.Work)

	var session struct {
		ID string `json:"id"`
	}
	serverPermissionJSON(t, client, baseURL+"/session"+query, http.MethodPost,
		map[string]string{"title": "AHT native active interruption"}, &session)

	if session.ID == "" {
		t.Fatal("native interruption session has no identity")
	}

	sessionURL := baseURL + "/session/" + url.PathEscape(session.ID)
	serverPermissionJSON(t, client, sessionURL+"/prompt_async"+query, http.MethodPost,
		map[string]any{
			"model": map[string]string{
				"providerID": harness.LaunchProviderName,
				"modelID":    fakeModel,
			},
			"parts": []map[string]string{{"type": "text", "text": compatibilityPrompt}},
		}, nil)

	host.awaitHold(t, process, 0)

	host.waitForActiveSession(t)

	var aborted bool
	serverPermissionJSON(t, client, sessionURL+"/abort"+query, http.MethodPost, nil, &aborted)

	if !aborted {
		t.Fatal("native session abort was rejected")
	}

	serverPermissionIdle(t, client, baseURL, query, session.ID, process)
}
