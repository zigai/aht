//go:build compatibility

package hostcompat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
)

// OpenCode 2 serves its HTTP API under /api behind basic auth; the operations
// below are the ones its /openapi.json publishes for sessions and permissions.
var errOpenCodeAPI = errors.New("native OpenCode API")

type openCodeServer struct {
	baseURL  string
	password string
	client   *http.Client
	process  *permissionProcess
}

func startOpenCodeServer(t *testing.T, host *isolatedHost, env []string) *openCodeServer {
	t.Helper()
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(secret)
	port := strconv.Itoa(freeLocalPort(t))
	env = append(slices.Clone(env), "OPENCODE_SERVER_PASSWORD="+password)
	command := host.command(t, env, "serve", "--hostname", "127.0.0.1", "--port", port)
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	server := &openCodeServer{
		baseURL:  "http://" + net.JoinHostPort("127.0.0.1", port),
		password: password,
		client: &http.Client{
			Transport:     transport,
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		process: startPermissionProcess(t, host, command),
	}
	server.waitReady(t)
	return server
}

func (server *openCodeServer) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, server.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	request.SetBasicAuth("opencode", server.password)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := server.client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("send %s %s: %w", method, path, err)
	}
	const maxBody = 4 << 20
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return 0, nil, fmt.Errorf("read %s %s: %w; close: %w", method, path, readErr, closeErr)
	}
	if len(data) > maxBody {
		return 0, nil, fmt.Errorf("%w: %s %s response exceeds 4 MiB", errOpenCodeAPI, method, path)
	}
	return response.StatusCode, data, nil
}

func (server *openCodeServer) call(t *testing.T, method, path string, body, result any) {
	t.Helper()
	status, data, err := server.do(t.Context(), method, path, body)
	if err != nil {
		t.Fatalf("native %s %s: %v", method, path, err)
	}
	if status < 200 || status >= 300 {
		t.Fatalf("native %s %s: HTTP %d: %s", method, path, status, data)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatalf("decoding native %s %s: %v: %s", method, path, err, data)
		}
	}
}

func (server *openCodeServer) waitReady(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var lastErr error
	for {
		status, data, err := server.do(ctx, http.MethodGet, "/api/session/active", nil)
		lastErr = err
		if err == nil {
			var active struct {
				Data map[string]any `json:"data"`
			}
			if status == http.StatusOK && json.Unmarshal(data, &active) == nil {
				return
			}
			lastErr = fmt.Errorf("%w: HTTP %d: %.200s", errOpenCodeAPI, status, data)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("native server not ready: %v (%v)", ctx.Err(), lastErr)
		case <-server.process.done:
			t.Fatalf("native server exited before ready: %v", server.process.waitErr())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (server *openCodeServer) startTurn(t *testing.T, host *isolatedHost, permissions []map[string]string) string {
	t.Helper()
	body := map[string]any{
		"location": map[string]string{"directory": host.launched.Work},
		"model":    map[string]string{"id": fakeModel, "providerID": harness.LaunchProviderName},
	}
	if permissions != nil {
		body["permissions"] = permissions
	}
	var session struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	server.call(t, http.MethodPost, "/api/session", body, &session)
	if session.Data.ID == "" {
		t.Fatal("native session creation returned no session ID")
	}
	server.call(t, http.MethodPost, "/api/session/"+url.PathEscape(session.Data.ID)+"/prompt",
		map[string]string{"text": compatibilityPrompt}, nil)
	return session.Data.ID
}

func (server *openCodeServer) waitIdle(t *testing.T, sessionID string) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		var active struct {
			Data map[string]any `json:"data"`
		}
		server.call(t, http.MethodGet, "/api/session/active", nil, &active)
		if _, running := active.Data[sessionID]; !running {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("native session did not become inactive")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-server.process.done:
			t.Fatalf("native server exited before the turn ended: %v", server.process.waitErr())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (server *openCodeServer) stop(t *testing.T) {
	t.Helper()
	// The fixture owns, stops, and joins the server process group on every
	// failure path too; do not signal any externally discovered PID.
	server.process.interrupt(t)
	select {
	case <-server.process.done:
	case <-time.After(5 * time.Second):
		t.Fatal("native server did not stop after interrupt")
	}
}

// Interrupt ends the active execution, not the durable session.
func (host *isolatedHost) runOpenCodeInterruption(t *testing.T, env []string) {
	t.Helper()
	server := startOpenCodeServer(t, host, env)
	sessionID := server.startTurn(t, host, nil)
	select {
	case hold := <-host.provider.Holds():
		if hold.Request.Index != 0 {
			t.Fatalf("first active request step = %d", hold.Request.Index)
		}
	case <-server.process.done:
		t.Fatalf("server exited before active request: %v", server.process.waitErr())
	case <-time.After(30 * time.Second):
		t.Fatal("native server did not reach active provider request")
	}
	host.waitForActiveSession(t)
	var interrupted struct {
		Interrupted bool `json:"interrupted"`
	}
	server.call(t, http.MethodPost, "/api/session/"+url.PathEscape(sessionID)+"/interrupt", nil, &interrupted)
	if !interrupted.Interrupted {
		t.Fatal("native session interrupt found no active execution")
	}
	server.waitIdle(t, sessionID)
}

func runOpenCodePermissionScenarios(t *testing.T, contract hostContract, oracle string) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, oracle, allow)
			server := startOpenCodeServer(t, host, host.lifecycleCommand(t).Env)
			sessionID := server.startTurn(t, host, []map[string]string{{"action": host.launched.ShellTool.Name, "resource": "*", "effect": "ask"}})
			pending := server.pendingPermission(t, sessionID)
			waiting := assertPermissionWaiting(t, host)
			if waiting.Observations.Native == nil || waiting.Observations.Native.SessionID != sessionID {
				t.Fatalf("AHT waiting session does not match native permission session %s: %#v", sessionID, waiting)
			}
			decision := "reject"
			if allow {
				decision = "once"
			}
			server.call(t, http.MethodPost, "/api/session/"+url.PathEscape(sessionID)+"/permission/"+url.PathEscape(pending.ID)+"/reply",
				map[string]string{"decision": decision}, nil)
			server.waitIdle(t, sessionID)
			server.assertToolResult(t, host, sessionID, pending.Source.ID, allow)
			assertPermissionOutcome(t, host, waiting, allow)
			server.stop(t)
		})
	}
}

type openCodePermission struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Source struct {
		ID string `json:"id"`
	} `json:"source"`
}

func (server *openCodeServer) pendingPermission(t *testing.T, sessionID string) openCodePermission {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		var pending struct {
			Data []openCodePermission `json:"data"`
		}
		server.call(t, http.MethodGet, "/api/session/"+url.PathEscape(sessionID)+"/permission", nil, &pending)
		if len(pending.Data) > 0 {
			permission := pending.Data[0]
			if permission.ID == "" || permission.Source.ID == "" {
				t.Fatalf("unexpected native permission request: %#v", permission)
			}
			return permission
		}
		select {
		case <-deadline.C:
			t.Fatal("native server did not request shell permission")
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-server.process.done:
			t.Fatalf("native server exited before permission request: %v", server.process.waitErr())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

type openCodeToolState struct {
	Status  string `json:"status"`
	Error   any    `json:"error"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type openCodeToolPart struct {
	Type  string            `json:"type"`
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	State openCodeToolState `json:"state"`
}

func (server *openCodeServer) toolPart(t *testing.T, sessionID, callID string) openCodeToolPart {
	t.Helper()
	var messages struct {
		Data []struct {
			Content []openCodeToolPart `json:"content"`
		} `json:"data"`
	}
	server.call(t, http.MethodGet, "/api/session/"+url.PathEscape(sessionID)+"/message", nil, &messages)
	for _, message := range messages.Data {
		for _, part := range message.Content {
			if part.Type == "tool" && part.ID == callID {
				return part
			}
		}
	}
	t.Fatalf("native session has no tool result for permission call %s", callID)
	return openCodeToolPart{}
}

func (server *openCodeServer) assertToolResult(t *testing.T, host *isolatedHost, sessionID, callID string, allow bool) {
	t.Helper()
	part := server.toolPart(t, sessionID, callID)
	if part.Name != host.launched.ShellTool.Name {
		t.Fatalf("permission resolved a different tool: %s", part.Name)
	}
	var output strings.Builder
	for _, content := range part.State.Content {
		output.WriteString(content.Text)
	}
	marked := strings.Contains(output.String(), compatibilityMarker)
	if allow && (part.State.Status != "completed" || !marked) {
		t.Fatalf("approved native tool did not complete marker command: %#v", part.State)
	}
	if !allow && (part.State.Status != "error" || part.State.Error == nil || marked) {
		t.Fatalf("rejected native tool did not report denial without execution: %#v", part.State)
	}
}
