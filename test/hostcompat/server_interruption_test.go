//go:build compatibility

package hostcompat

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The native abort endpoint ends the active turn, not the durable session.
// https://opencode.ai/docs/server/#sessions
// https://github.com/Kilo-Org/kilocode/blob/main/packages/sdk/js/src/v2/gen/types.gen.ts
func (host isolatedHost) runServerInterruption(t *testing.T, env []string) {
	t.Helper()
	baseURL, client, process := startNativeServer(t, host, env)
	query := "?directory=" + url.QueryEscape(host.work)
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
			"model": map[string]string{"providerID": "aht-compat", "modelID": "compat"},
			"parts": []map[string]string{{"type": "text", "text": compatibilityPrompt}},
		}, nil)
	select {
	case step := <-host.provider.checkpoints:
		if step != 0 {
			t.Fatalf("first active request step = %d", step)
		}
	case <-process.done:
		t.Fatalf("server exited before active request: %v", process.waitErr())
	case <-time.After(30 * time.Second):
		t.Fatal("native server did not reach active provider request")
	}
	host.waitForActiveSession(t)
	var aborted bool
	serverPermissionJSON(t, client, sessionURL+"/abort"+query, http.MethodPost, nil, &aborted)
	if !aborted {
		t.Fatal("native session abort was rejected")
	}
	serverPermissionIdle(t, client, baseURL, query, session.ID, process)
}
