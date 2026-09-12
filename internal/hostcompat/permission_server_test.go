//go:build compatibility

package hostcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

// Both hosts publish these HTTP operations in their native SDK. Polling the
// pending-permission resource avoids losing permission.asked during SSE setup.
func runServerPermissionScenarios(t *testing.T, contract hostContract) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, allow)
			configured, setup := host.lifecycleCommand(t)
			if len(setup) != 0 {
				t.Fatal("server permission driver does not expect setup commands")
			}
			configName := "opencode.json"
			if contract.ID == registry.HarnessKilo {
				configName = "kilo.json"
			}
			host.writeFile(t, filepath.Join(host.work, configName), `{"permission":{"bash":"ask"}}`)

			baseURL, client, process := startNativeServer(t, host, configured.Env)
			query := "?directory=" + url.QueryEscape(host.work)
			var session struct {
				ID string `json:"id"`
			}
			serverPermissionJSON(t, client, baseURL+"/session"+query, http.MethodPost,
				map[string]any{"title": "AHT native permission " + name}, &session)
			if session.ID == "" {
				t.Fatal("native session creation returned no session ID")
			}
			sessionURL := baseURL + "/session/" + url.PathEscape(session.ID)
			serverPermissionJSON(t, client, sessionURL+"/prompt_async"+query, http.MethodPost,
				map[string]any{
					"model": map[string]string{"providerID": "aht-compat", "modelID": "compat"},
					"parts": []map[string]string{{"type": "text", "text": compatibilityPrompt}},
				}, nil)

			pending := serverPermissionPending(t, client, baseURL, query, session.ID, process)
			waiting := assertPermissionWaiting(t, host)
			if waiting.Observations.Native == nil || waiting.Observations.Native.SessionID != session.ID {
				t.Fatalf("AHT waiting session does not match native permission session %s: %#v", session.ID, waiting)
			}
			reply := "reject"
			if allow {
				reply = "once"
			}
			serverPermissionJSON(t, client, baseURL+"/permission/"+url.PathEscape(pending.ID)+"/reply"+query,
				http.MethodPost, map[string]string{"reply": reply}, nil)
			serverPermissionIdle(t, client, baseURL, query, session.ID, process)
			serverPermissionToolResult(t, client, sessionURL+"/message"+query, pending.Tool.CallID, allow)
			assertPermissionOutcome(t, host, waiting, allow)
			// The fixture owns, stops, and joins the server process group on every
			// failure path too; do not signal any externally discovered PID.
			process.interrupt(t)
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				t.Fatal("native permission server did not stop after interrupt")
			}
		})
	}
}

func startNativeServer(t *testing.T, host isolatedHost, env []string) (string, *http.Client, *permissionProcess) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, err := net.SplitHostPort(address)
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	command := host.command(env, "serve", "--hostname", "127.0.0.1", "--port", port)
	process := startPermissionProcess(t, host, command)
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{
		Transport:     transport,
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(transport.CloseIdleConnections)
	baseURL := "http://" + address
	serverPermissionReady(t, client, baseURL, process)
	return baseURL, client, process
}

type serverPendingPermission struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionID"`
	Permission string `json:"permission"`
	Tool       struct {
		CallID string `json:"callID"`
	} `json:"tool"`
}

func serverPermissionReady(t *testing.T, client *http.Client, baseURL string, process *permissionProcess) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/global/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		lastErr = err
		if err == nil {
			var health struct {
				Healthy bool `json:"healthy"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health)
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && closeErr == nil && health.Healthy {
				return
			}
			lastErr = fmt.Errorf("health status %d, decode %v, close %v, healthy %v", response.StatusCode, decodeErr, closeErr, health.Healthy)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("native server not ready: %v (%v)", ctx.Err(), lastErr)
		case <-process.done:
			t.Fatalf("native server exited before ready: %v", process.waitErr())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func serverPermissionJSON(t *testing.T, client *http.Client, endpoint, method string, body, result any) {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), method, endpoint, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("native %s %s: %v", method, endpoint, err)
	}
	const maxBody = 4 << 20
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("reading native API response: %v; close: %v", readErr, closeErr)
	}
	if len(data) > maxBody {
		t.Fatal("native API response exceeds 4 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("native %s %s: HTTP %d: %s", method, endpoint, response.StatusCode, data)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatalf("decoding native API response: %v: %s", err, data)
		}
	}
}

func serverPermissionPending(t *testing.T, client *http.Client, baseURL, query, sessionID string, process *permissionProcess) serverPendingPermission {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		var pending []serverPendingPermission
		serverPermissionJSON(t, client, baseURL+"/permission"+query, http.MethodGet, nil, &pending)
		for _, permission := range pending {
			if permission.SessionID != sessionID {
				continue
			}
			if permission.Permission != "bash" || permission.ID == "" || permission.Tool.CallID == "" {
				t.Fatalf("unexpected native permission request: %#v", permission)
			}
			return permission
		}
		select {
		case <-deadline.C:
			t.Fatal("native server did not request bash permission")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-process.done:
			t.Fatalf("native server exited before permission request: %v", process.waitErr())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func serverPermissionIdle(t *testing.T, client *http.Client, baseURL, query, sessionID string, process *permissionProcess) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		var statuses map[string]struct {
			Type string `json:"type"`
		}
		serverPermissionJSON(t, client, baseURL+"/session/status"+query, http.MethodGet, nil, &statuses)
		status, exists := statuses[sessionID]
		// Native servers remove idle sessions from the active-status map.
		if !exists || status.Type == "idle" {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("native session did not finish after permission decision")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-process.done:
			t.Fatalf("native server exited before completing permission turn: %v", process.waitErr())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func serverPermissionToolResult(t *testing.T, client *http.Client, endpoint, callID string, allow bool) {
	t.Helper()
	var messages []struct {
		Parts []struct {
			Type   string `json:"type"`
			CallID string `json:"callID"`
			Tool   string `json:"tool"`
			State  struct {
				Status string `json:"status"`
				Output string `json:"output"`
				Error  string `json:"error"`
			} `json:"state"`
		} `json:"parts"`
	}
	serverPermissionJSON(t, client, endpoint, http.MethodGet, nil, &messages)
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Type != "tool" || part.CallID != callID {
				continue
			}
			if part.Tool != "bash" {
				t.Fatalf("permission resolved a different tool: %s", part.Tool)
			}
			if allow {
				if part.State.Status != "completed" || !strings.Contains(part.State.Output, "aht-compat-marker") {
					t.Fatalf("approved native tool did not complete marker command: %#v", part.State)
				}
			} else if part.State.Status != "error" || part.State.Error == "" || strings.Contains(part.State.Output, "aht-compat-marker") {
				t.Fatalf("rejected native tool did not report denial without execution: %#v", part.State)
			}
			return
		}
	}
	t.Fatalf("native session has no tool result for permission call %s", callID)
}
