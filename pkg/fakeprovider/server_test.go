package fakeprovider_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const (
	waitTimeout  = 10 * time.Second
	probeTimeout = 50 * time.Millisecond
)

var (
	errScript = errors.New("script failed")
	errOther  = errors.New("other script failure")
)

func startServer(t *testing.T, options fakeprovider.Options) *fakeprovider.Server {
	t.Helper()
	server, err := fakeprovider.Start(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close fake provider: %v", err)
		}
	})
	return server
}

func send(ctx context.Context, method, target, body string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("new request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, string(data), fmt.Errorf("read response: %w", err)
	}
	return response.StatusCode, string(data), nil
}

func postJSON(t *testing.T, target, body string) (int, string) {
	t.Helper()
	status, data, err := send(t.Context(), http.MethodPost, target, body)
	if err != nil {
		t.Fatal(err)
	}
	return status, data
}

func pathFor(protocol fakeprovider.Protocol) string {
	switch protocol {
	case fakeprovider.ProtocolAnthropicMessages:
		return "/v1/messages"
	case fakeprovider.ProtocolOpenAIChat:
		return "/v1/chat/completions"
	case fakeprovider.ProtocolOpenAIResponses:
		return "/v1/responses"
	}
	panic("unknown protocol " + string(protocol))
}

func replyText(t *testing.T, protocol fakeprovider.Protocol, body string) string {
	t.Helper()
	var value struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("decode reply: %v\n%s", err, body)
	}
	switch protocol {
	case fakeprovider.ProtocolAnthropicMessages:
		if len(value.Content) > 0 {
			return value.Content[0].Text
		}
	case fakeprovider.ProtocolOpenAIChat:
		if len(value.Choices) > 0 {
			return value.Choices[0].Message.Content
		}
	case fakeprovider.ProtocolOpenAIResponses:
		if len(value.Output) > 0 && len(value.Output[0].Content) > 0 {
			return value.Output[0].Content[0].Text
		}
	}
	return ""
}

func closeAndRequests(t *testing.T, server *fakeprovider.Server) []fakeprovider.Request {
	t.Helper()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	return server.Requests()
}

func TestStartRejectsTakenAddress(t *testing.T) {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if _, err := fakeprovider.Start(fakeprovider.Options{Address: listener.Addr().String()}); err == nil {
		t.Fatal("Start succeeded on an address that is already in use")
	}
}

func TestStartRejectsEmptySideMarker(t *testing.T) {
	if _, err := fakeprovider.Start(fakeprovider.Options{SideMarkers: []string{""}}); err == nil {
		t.Fatal("Start accepted an empty side marker, which would classify every request as a side request")
	}
}

func TestURLIsOriginWithoutVersionPrefix(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	parsed, err := url.Parse(server.URL())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.Path != "" {
		t.Fatalf("URL = %q, want http://127.0.0.1:PORT", server.URL())
	}
	status, _, err := send(t.Context(), http.MethodGet, server.URL()+"/", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("root probe = %d, %v; want 200", status, err)
	}
}

func TestProbesAreAnsweredWithoutRecording(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	base := server.URL()
	probes := []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{name: "head", method: http.MethodHead, path: "/v1/responses"},
		{name: "root", method: http.MethodGet, path: "/"},
		{name: "model list", method: http.MethodGet, path: "/v1/models", want: `"fake"`},
		{name: "model", method: http.MethodGet, path: "/v1/models/fake", want: `"fake"`},
		{name: "show", method: http.MethodPost, path: "/api/show", body: `{}`, want: `"tools"`},
		{name: "count tokens", method: http.MethodPost, path: "/v1/messages/count_tokens", body: `{}`, want: `"input_tokens":1`},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			status, body, err := send(t.Context(), probe.method, base+probe.path, probe.body)
			if err != nil {
				t.Fatal(err)
			}
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			if probe.want != "" && !strings.Contains(body, probe.want) {
				t.Fatalf("body = %s, want it to contain %s", body, probe.want)
			}
		})
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 0 || len(server.SideRequests()) != 0 {
		t.Fatalf("probes were recorded: %d conversation, %d side", len(requests), len(server.SideRequests()))
	}
	if err := server.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeBetweenTurnsDoesNotAdvanceConversation(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	status, _, err := send(t.Context(), http.MethodHead, server.URL()+"/", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("probe = %d, %v", status, err)
	}
	postJSON(t, server.URL()+"/v1/responses", `{"input":"first"}`)
	status, _, err = send(t.Context(), http.MethodGet, server.URL()+"/", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("probe = %d, %v", status, err)
	}
	postJSON(t, server.URL()+"/v1/responses", `{"input":"second"}`)
	requests := closeAndRequests(t, server)
	if len(requests) != 2 || requests[1].Index != 1 {
		t.Fatalf("requests = %+v, want two conversation requests with indexes 0 and 1", requests)
	}
}

func TestUnknownEndpointIsAnError(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	status, _ := postJSON(t, server.URL()+"/v1/embeddings", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if err := server.Err(); err == nil {
		t.Fatal("unknown endpoint did not set Err")
	}
	if requests := closeAndRequests(t, server); len(requests) != 0 {
		t.Fatalf("unknown endpoint was recorded as a conversation request: %+v", requests)
	}
}

func TestNonPostModelRequestIsRejected(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	status, _, err := send(t.Context(), http.MethodGet, server.URL()+"/v1/responses", "")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", status)
	}
	if server.Err() == nil {
		t.Fatal("GET on a model endpoint did not set Err")
	}
}

func TestMalformedBodiesAreRejectedAndNotRecorded(t *testing.T) {
	oversized := `{"input":"` + strings.Repeat("a", 4<<20) + `"}`
	tests := []struct {
		name         string
		body         string
		refusedEarly bool
	}{
		{name: "not json", body: `{"input"`},
		{name: "array", body: `[]`},
		{name: "null", body: `null`},
		{name: "oversized", body: oversized, refusedEarly: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{})
			status, _, err := send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", test.body)
			if err != nil && !test.refusedEarly {
				t.Fatal(err)
			}
			if err == nil && status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
			requests := closeAndRequests(t, server)
			if len(requests) != 0 {
				t.Fatalf("malformed body was recorded: %+v", requests)
			}
			if server.Err() == nil {
				t.Fatal("malformed body did not set Err")
			}
		})
	}
}

func TestSideRequestsAreAnsweredApartAndNeverHeld(t *testing.T) {
	server := startServer(t, fakeprovider.Options{Hold: true, SideMarkers: []string{"custom-title-cue"}})
	markers := []string{
		"You are a title generator",
		"ultra-short dashboard line",
		"generate_session_title",
		"Generate a session title",
		"Write the title in the predominant language",
		"Generate a short, descriptive title",
		"custom-title-cue",
	}
	for _, marker := range markers {
		body := fmt.Sprintf(`{"messages":[{"role":"system","content":%q}]}`, marker)
		status, data := postJSON(t, server.URL()+"/v1/chat/completions", body)
		if status != http.StatusOK || replyText(t, fakeprovider.ProtocolOpenAIChat, data) != "Fake session" {
			t.Fatalf("side request %q = %d %s, want the title reply", marker, status, data)
		}
	}
	if got := len(server.SideRequests()); got != len(markers) {
		t.Fatalf("side requests = %d, want %d", got, len(markers))
	}
	if requests := closeAndRequests(t, server); len(requests) != 0 {
		t.Fatalf("side requests were recorded as conversation: %+v", requests)
	}
}

func TestSideClassificationByToolAndStructuredOutput(t *testing.T) {
	tests := []struct {
		name     string
		protocol fakeprovider.Protocol
		body     string
		want     string
	}{
		{
			name:     "session title tool",
			protocol: fakeprovider.ProtocolAnthropicMessages,
			body:     `{"tools":[{"name":"session_title"}],"messages":[]}`,
			want:     "Fake session",
		},
		{
			name:     "session title function",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body:     `{"tools":[{"type":"function","function":{"name":"session_title"}}],"messages":[]}`,
			want:     "Fake session",
		},
		{
			name:     "responses structured output fills required string fields",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body:     `{"input":"x","text":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"},"count":{"type":"integer"}},"required":["title","count"]}}}}`,
			want:     `{"title":"Fake session"}`,
		},
		{
			name:     "chat structured output without required list",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body:     `{"messages":[],"response_format":{"type":"json_schema","json_schema":{"name":"t","schema":{"type":"object","properties":{"name":{"type":"string"}}}}}}`,
			want:     `{"name":"Fake session"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{Hold: true})
			status, data := postJSON(t, server.URL()+pathFor(test.protocol), test.body)
			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, data)
			}
			if got := replyText(t, test.protocol, data); got != test.want {
				t.Fatalf("reply text = %s, want %s", got, test.want)
			}
			if got := len(server.SideRequests()); got != 1 {
				t.Fatalf("side requests = %d, want 1", got)
			}
		})
	}
}

func TestRequestIndexesFollowArrivalOrder(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	for _, text := range []string{"one", "two", "three"} {
		postJSON(t, server.URL()+"/v1/chat/completions", fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, text))
	}
	requests := closeAndRequests(t, server)
	want := []string{"one", "two", "three"}
	if len(requests) != len(want) {
		t.Fatalf("requests = %d, want %d", len(requests), len(want))
	}
	for i, request := range requests {
		if request.Index != i || request.UserText != want[i] || request.Outcome != fakeprovider.OutcomeCompleted {
			t.Fatalf("request %d = index %d text %q outcome %q", i, request.Index, request.UserText, request.Outcome)
		}
		if request.Kind != fakeprovider.KindConversation || request.Protocol != fakeprovider.ProtocolOpenAIChat {
			t.Fatalf("request %d kind = %q protocol = %q", i, request.Kind, request.Protocol)
		}
	}
}

func TestRequestSnapshotsAreIndependentOfTheServer(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	postJSON(t, server.URL()+"/v1/chat/completions", `{"model":"m","tools":[{"name":"lookup"}],"messages":[{"role":"user","content":"hi"}]}`)
	first := server.Requests()
	first[0].Tools[0] = "changed"
	first[0].Body[0] = 'X'
	if got := server.Requests()[0]; got.Tools[0] != "lookup" || got.Body[0] != '{' {
		t.Fatalf("mutating a snapshot changed the server state: tools %v body %.10s", got.Tools, got.Body)
	}
}

func TestScriptErrorRejectsRequestAndKeepsFirstError(t *testing.T) {
	var calls atomic.Int32
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		if calls.Add(1) == 1 {
			return fakeprovider.Reply{}, errScript
		}
		return fakeprovider.Reply{}, errOther
	})
	server := startServer(t, fakeprovider.Options{Script: script})
	for range 2 {
		if status, _ := postJSON(t, server.URL()+"/v1/chat/completions", `{"messages":[]}`); status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
	}
	if err := server.Err(); !errors.Is(err, errScript) {
		t.Fatalf("Err = %v, want the first script error", err)
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 2 || requests[0].Outcome != fakeprovider.OutcomeRejected || requests[1].Outcome != fakeprovider.OutcomeRejected {
		t.Fatalf("outcomes = %+v, want both rejected", requests)
	}
}

func TestInvalidRepliesAreScriptErrors(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
		reply  fakeprovider.Reply
	}{
		{name: "tool call without id", reply: fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{Name: "lookup"}}}},
		{name: "tool call without name", reply: fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{ID: "c"}}}},
		{name: "error status below 400", reply: fakeprovider.Reply{Error: &fakeprovider.ErrorReply{Status: 200, Message: "ok"}}},
		{name: "error with text", reply: fakeprovider.Reply{Text: "x", Error: &fakeprovider.ErrorReply{Status: 500, Message: "boom"}}},
		{name: "mid-stream hold without stream", reply: fakeprovider.Reply{Text: "x", HoldMidStream: true}},
		{name: "mid-stream hold with disconnect", stream: true, reply: fakeprovider.Reply{Text: "x", HoldMidStream: true, Disconnect: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
				return test.reply, nil
			})
			server := startServer(t, fakeprovider.Options{Script: script})
			body := fmt.Sprintf(`{"stream":%t,"messages":[]}`, test.stream)
			if status, _ := postJSON(t, server.URL()+"/v1/chat/completions", body); status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
			if server.Err() == nil {
				t.Fatal("invalid reply did not set Err")
			}
		})
	}
}

func TestReplyErrorIsProviderShapedAndNotAProviderFailure(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Error: &fakeprovider.ErrorReply{Status: http.StatusTooManyRequests, Message: "slow down"}}, nil
	})
	for _, protocol := range []fakeprovider.Protocol{
		fakeprovider.ProtocolAnthropicMessages,
		fakeprovider.ProtocolOpenAIChat,
		fakeprovider.ProtocolOpenAIResponses,
	} {
		t.Run(string(protocol), func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{Script: script})
			status, data := postJSON(t, server.URL()+pathFor(protocol), `{"stream":true,"messages":[]}`)
			if status != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", status)
			}
			if !strings.Contains(data, "slow down") {
				t.Fatalf("body = %s, want the scripted message", data)
			}
			if err := server.Err(); err != nil {
				t.Fatalf("scripted error reply set Err: %v", err)
			}
			requests := closeAndRequests(t, server)
			if len(requests) != 1 || requests[0].Outcome != fakeprovider.OutcomeRejected {
				t.Fatalf("requests = %+v, want one rejected", requests)
			}
		})
	}
}

func TestResetRestartsSessionAndClearsErrors(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	postJSON(t, server.URL()+"/v1/chat/completions", `[]`)
	if server.Err() == nil {
		t.Fatal("invalid body did not set Err")
	}
	server.Reset(nil)
	if err := server.Err(); err != nil {
		t.Fatalf("Err after Reset = %v", err)
	}
	postJSON(t, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"user","content":"again"}]}`)
	requests := closeAndRequests(t, server)
	if len(requests) != 1 || requests[0].Index != 0 || requests[0].UserText != "again" {
		t.Fatalf("requests after Reset = %+v, want one request at index 0", requests)
	}
}

func TestHoldBeforeReplyWaitsForRelease(t *testing.T) {
	var calls atomic.Int32
	script := fakeprovider.ScriptFunc(func(request fakeprovider.Request) (fakeprovider.Reply, error) {
		calls.Add(1)
		return fakeprovider.Reply{Text: "echo " + request.UserText, ToolCalls: nil, Error: nil, HoldMidStream: false, Disconnect: false}, nil
	})
	server := startServer(t, fakeprovider.Options{Hold: true, Script: script})
	type result struct {
		status int
		body   string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		status, body, err := send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`)
		done <- result{status: status, body: body, err: err}
	}()
	hold := receiveHold(t, server)
	if hold.Stage != fakeprovider.StageBeforeReply || hold.Request.UserText != "hi" {
		t.Fatalf("hold = stage %q text %q", hold.Stage, hold.Request.UserText)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("script ran %d times before release", got)
	}
	hold.Release()
	hold.Release()
	select {
	case <-hold.Done():
	case <-time.After(waitTimeout):
		t.Fatal("Done did not close after Release")
	}
	got := <-done
	if got.err != nil || got.status != http.StatusOK || !strings.Contains(got.body, "echo hi") {
		t.Fatalf("released reply = %d %v %s", got.status, got.err, got.body)
	}
	if calls.Load() != 1 {
		t.Fatalf("script ran %d times, want 1", calls.Load())
	}
}

func TestClientCancelWhileHeldCancelsRequest(t *testing.T) {
	var calls atomic.Int32
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		calls.Add(1)
		return fakeprovider.Reply{}, nil
	})
	server := startServer(t, fakeprovider.Options{Hold: true, Script: script})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := send(ctx, http.MethodPost, server.URL()+"/v1/chat/completions", `{"messages":[]}`)
		done <- err
	}()
	hold := receiveHold(t, server)
	cancel()
	select {
	case <-hold.Done():
	case <-time.After(waitTimeout):
		t.Fatal("Done did not close after the client went away")
	}
	if err := <-done; err == nil {
		t.Fatal("canceled request returned a response")
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 1 || requests[0].Outcome != fakeprovider.OutcomeCanceled {
		t.Fatalf("requests = %+v, want one canceled", requests)
	}
	if calls.Load() != 0 {
		t.Fatal("script ran for a request the client abandoned")
	}
}

func TestCloseReleasesHeldRequests(t *testing.T) {
	server := startServer(t, fakeprovider.Options{Hold: true})
	done := make(chan error, 1)
	go func() {
		_, _, err := send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", `{"messages":[]}`)
		done <- err
	}()
	receiveHold(t, server)
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Close did not return while a request was held")
	}
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("held client was not released by Close")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	if got := server.Requests(); len(got) != 1 || got[0].Outcome != fakeprovider.OutcomeCanceled {
		t.Fatalf("requests after Close = %+v, want one canceled", got)
	}
}

func TestResetAbandonsHeldRequestsAndStartsNewSession(t *testing.T) {
	server := startServer(t, fakeprovider.Options{Hold: true})
	abandoned := make(chan int, 1)
	go func() {
		status, _, _ := send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"user","content":"old"}]}`)
		abandoned <- status
	}()
	receiveHold(t, server)
	server.Reset(nil)
	select {
	case status := <-abandoned:
		if status != http.StatusServiceUnavailable {
			t.Fatalf("abandoned request status = %d, want 503", status)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Reset did not release the held request")
	}
	if got := server.Requests(); len(got) != 0 {
		t.Fatalf("requests after Reset = %+v, want none", got)
	}
	go func() {
		_, _, _ = send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"user","content":"new"}]}`)
	}()
	hold := receiveHold(t, server)
	if hold.Request.Index != 0 || hold.Request.UserText != "new" {
		t.Fatalf("hold after Reset = index %d text %q, want index 0 text new", hold.Request.Index, hold.Request.UserText)
	}
	hold.Release()
}

func TestWaitRequestsReturnsOnceCountIsReached(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	type result struct {
		requests []fakeprovider.Request
		err      error
	}
	done := make(chan result, 1)
	go func() {
		requests, err := server.WaitRequests(t.Context(), 2)
		done <- result{requests: requests, err: err}
	}()
	postJSON(t, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"user","content":"a"}]}`)
	postJSON(t, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"user","content":"b"}]}`)
	got := <-done
	if got.err != nil || len(got.requests) != 2 || got.requests[1].UserText != "b" {
		t.Fatalf("WaitRequests = %+v, %v", got.requests, got.err)
	}
}

func TestWaitRequestsStopsOnContextSideRequestsAndClose(t *testing.T) {
	t.Run("context", func(t *testing.T) {
		server := startServer(t, fakeprovider.Options{})
		ctx, cancel := context.WithTimeout(t.Context(), probeTimeout)
		defer cancel()
		if _, err := server.WaitRequests(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want deadline exceeded", err)
		}
	})
	t.Run("side requests do not count", func(t *testing.T) {
		server := startServer(t, fakeprovider.Options{})
		postJSON(t, server.URL()+"/v1/chat/completions", `{"messages":[{"role":"system","content":"Generate a session title"}]}`)
		ctx, cancel := context.WithTimeout(t.Context(), probeTimeout)
		defer cancel()
		if _, err := server.WaitRequests(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want deadline exceeded", err)
		}
	})
	t.Run("close", func(t *testing.T) {
		server := startServer(t, fakeprovider.Options{})
		done := make(chan error, 1)
		go func() {
			_, err := server.WaitRequests(t.Context(), 1)
			done <- err
		}()
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("WaitRequests returned no error after Close")
			}
		case <-time.After(waitTimeout):
			t.Fatal("WaitRequests did not return after Close")
		}
	})
}

func TestWaitRequestsRejectsNegativeCount(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	if _, err := server.WaitRequests(t.Context(), -1); err == nil {
		t.Fatal("negative count was accepted")
	}
}

func TestConcurrentConversationsGetDistinctIndexes(t *testing.T) {
	const turns = 20
	server := startServer(t, fakeprovider.Options{})
	var wg sync.WaitGroup
	for i := range turns {
		wg.Go(func() {
			body := fmt.Sprintf(`{"messages":[{"role":"user","content":"turn-%d"}]}`, i)
			status, _, err := send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", body)
			if err != nil || status != http.StatusOK {
				t.Errorf("turn %d = %d, %v", i, status, err)
			}
		})
	}
	wg.Wait()
	requests := closeAndRequests(t, server)
	seen := make(map[int]bool)
	for _, request := range requests {
		seen[request.Index] = true
	}
	if len(requests) != turns || len(seen) != turns {
		t.Fatalf("requests = %d with %d distinct indexes, want %d of each", len(requests), len(seen), turns)
	}
}

func receiveHold(t *testing.T, server *fakeprovider.Server) *fakeprovider.Hold {
	t.Helper()
	select {
	case hold := <-server.Holds():
		return hold
	case <-time.After(waitTimeout):
		t.Fatal("no hold delivered")
		return nil
	}
}
