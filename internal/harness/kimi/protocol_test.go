package kimi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/zigai/aht/internal/harness/kimi"
	"github.com/zigai/aht/pkg/registry"
)

func host(t *testing.T, p *kimi.Protocol, line string, want registry.Activity) {
	t.Helper()
	update, changed, err := p.ObserveHost([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	wantTransition := struct {
		Changed  bool
		Activity registry.Activity
	}{Changed: want != "", Activity: want}
	gotTransition := struct {
		Changed  bool
		Activity registry.Activity
	}{Changed: changed, Activity: update.Activity}
	if diff := cmp.Diff(wantTransition, gotTransition); diff != "" {
		t.Fatalf("activity transition mismatch (-want +got):\n%s", diff)
	}
}

func event(kind, payload string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"event","params":{"type":%q,"payload":%s}}`, kind, payload)
}

func request(kind, id, tool, extra string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"request","id":%q,"params":{"type":%q,"payload":{"id":%q,"tool_call_id":%q%s}}}`, id, kind, id, tool, extra)
}

func begin(t *testing.T, p *kimi.Protocol) {
	t.Helper()
	p.ObserveClient([]byte(`{"jsonrpc":"2.0","method":"prompt","id":"turn","params":{"user_input":"hello"}}`))
	host(t, p, event("TurnBegin", `{"user_input":"hello"}`), registry.ActivityRunning)
}

func TestConcurrentApprovalsRequireNativeResolution(t *testing.T) {
	var p kimi.Protocol
	begin(t, &p)
	host(t, &p, request("ApprovalRequest", "a", "tool-a", ""), registry.ActivityWaiting)
	host(t, &p, request("ApprovalRequest", "b", "tool-b", ""), registry.ActivityWaiting)
	p.ObserveClient([]byte(`{"jsonrpc":"2.0","id":"a","result":{"request_id":"a","response":"approve"}}`))
	host(t, &p, event("StepBegin", `{"n":2}`), registry.ActivityWaiting)
	host(t, &p, event("ApprovalResponse", `{"request_id":"unrelated","response":"approve"}`), "")
	host(t, &p, event("ApprovalResponse", `{"request_id":"a","response":"approve"}`), registry.ActivityWaiting)
	p.ObserveClient([]byte(`{"jsonrpc":"2.0","id":"b","error":{"code":-32603,"message":"dismissed"}}`))
	host(t, &p, event("ContentPart", `{"type":"text","text":"concurrent output"}`), "")
	host(t, &p, event("ApprovalResponse", `{"request_id":"b","response":"reject"}`), registry.ActivityRunning)
	host(t, &p, event("TurnEnd", `{}`), registry.ActivityIdle)
}

func TestApprovalResolutionOrderPreservesWaitingInvariant(t *testing.T) {
	type transition struct {
		Changed  bool
		Activity registry.Activity
	}

	rapid.Check(t, func(rt *rapid.T) {
		ids := rapid.SliceOfNDistinct(
			rapid.StringMatching(`[a-z][a-z0-9]{0,7}`),
			1,
			12,
			func(id string) string { return id },
		).Draw(rt, "ids")
		order := rapid.Permutation(ids).Draw(rt, "resolution_order")

		var protocol kimi.Protocol
		protocol.ObserveClient([]byte(`{"jsonrpc":"2.0","method":"prompt","id":"turn"}`))
		if _, _, err := protocol.ObserveHost([]byte(event("TurnBegin", `{}`))); err != nil {
			rt.Fatalf("starting turn: %v", err)
		}
		for _, id := range ids {
			if _, _, err := protocol.ObserveHost([]byte(request("ApprovalRequest", id, "tool-"+id, ""))); err != nil {
				rt.Fatalf("adding approval %q: %v", id, err)
			}
		}

		got := make([]transition, 0, len(order))
		want := make([]transition, 0, len(order))
		for index, id := range order {
			update, changed, err := protocol.ObserveHost([]byte(event("ApprovalResponse", fmt.Sprintf(`{"request_id":%q,"response":"approve"}`, id))))
			if err != nil {
				rt.Fatalf("resolving approval %q: %v", id, err)
			}

			activity := registry.ActivityWaiting
			if index == len(order)-1 {
				activity = registry.ActivityRunning
			}
			got = append(got, transition{Changed: changed, Activity: update.Activity})
			want = append(want, transition{Changed: true, Activity: activity})
		}

		if diff := cmp.Diff(want, got); diff != "" {
			rt.Fatalf("resolution transitions mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestQuestionWaitsForCorrelatedNativeToolCompletion(t *testing.T) {
	var p kimi.Protocol
	begin(t, &p)
	host(t, &p, request("QuestionRequest", "q", "ask", `,"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]}]`), registry.ActivityWaiting)
	p.ObserveClient([]byte(`{"jsonrpc":"2.0","id":"q","result":{"request_id":"q","answers":{}}}`))
	host(t, &p, event("ToolResult", `{"tool_call_id":"other","return_value":{"is_error":false}}`), "")
	host(t, &p, event("SubagentEvent", `{"agent_id":"child","event":{"type":"TurnEnd","payload":{}}}`), "")
	host(t, &p, event("SubagentEvent", `{"agent_id":"child","event":{"type":"ToolResult","payload":{"tool_call_id":"ask","return_value":{"is_error":true}}}}`), registry.ActivityRunning)
}

func TestAbortedTurnClearsForegroundPendingRequests(t *testing.T) {
	for _, terminal := range []struct {
		name string
		line string
		want registry.Activity
	}{
		{"canceled", `{"jsonrpc":"2.0","id":"turn","result":{"status":"canceled"}}`, registry.ActivityInterrupted},
		{"error", `{"jsonrpc":"2.0","id":"turn","error":{"code":-32003,"message":"provider secret"}}`, registry.ActivityFailed},
		{"step limit", `{"jsonrpc":"2.0","id":"turn","result":{"status":"max_steps_reached","steps":1}}`, registry.ActivityInterrupted},
		{"finished", `{"jsonrpc":"2.0","id":"turn","result":{"status":"finished"}}`, registry.ActivityIdle},
		{"interrupted", event("StepInterrupted", `{}`), registry.ActivityInterrupted},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			var p kimi.Protocol
			begin(t, &p)
			host(t, &p, request("ApprovalRequest", "a", "tool", ""), registry.ActivityWaiting)
			host(t, &p, request("QuestionRequest", "q", "ask", ""), registry.ActivityWaiting)
			p.ObserveClient([]byte(`{"jsonrpc":"2.0","method":"cancel","id":"cancel"}`))
			host(t, &p, `{"jsonrpc":"2.0","id":"cancel","result":{}}`, "")
			host(t, &p, terminal.line, terminal.want)
			host(t, &p, event("ApprovalResponse", `{"request_id":"a","response":"reject"}`), "")
		})
	}
}

func TestBackgroundApprovalSurvivesForegroundTerminal(t *testing.T) {
	var p kimi.Protocol
	begin(t, &p)
	host(t, &p, request("ApprovalRequest", "bg", "tool", `,"source_kind":"background_agent"`), registry.ActivityWaiting)
	// A foreground Stop hook may have published idle since the request.
	host(t, &p, `{"jsonrpc":"2.0","id":"turn","result":{"status":"finished"}}`, registry.ActivityWaiting)
	host(t, &p, event("ApprovalResponse", `{"request_id":"bg","response":"approve"}`), registry.ActivityRunning)
}

func TestReplayCannotResurrectHistoricalWaiting(t *testing.T) {
	for _, terminal := range []string{
		`{"jsonrpc":"2.0","id":"history","result":{"status":"finished","events":3,"requests":1}}`,
		`{"jsonrpc":"2.0","id":"history","result":{"status":"canceled","events":3,"requests":1}}`,
		`{"jsonrpc":"2.0","id":"history","error":{"code":-32603,"message":"replay failed"}}`,
	} {
		var p kimi.Protocol
		p.ObserveClient([]byte(`{"jsonrpc":"2.0","method":"replay","id":"history"}`))
		host(t, &p, event("TurnBegin", `{"user_input":"old"}`), "")
		host(t, &p, request("ApprovalRequest", "old", "old-tool", ""), "")
		host(t, &p, request("ApprovalRequest", "ambiguous-background", "bg-tool", `,"source_kind":"background_agent"`), "")
		host(t, &p, event("StepInterrupted", `{}`), "")
		host(t, &p, terminal, "")
		begin(t, &p)
		host(t, &p, event("ApprovalResponse", `{"request_id":"old","response":"reject"}`), "")
		host(t, &p, event("TurnEnd", `{}`), registry.ActivityIdle)
	}
}

func TestRejectedConcurrentRPCDoesNotEndLiveWait(t *testing.T) {
	var p kimi.Protocol
	begin(t, &p)
	p.ObserveClient([]byte(`{"jsonrpc":"2.0","method":"replay","id":"history"}`))
	p.ObserveClient([]byte(`{"jsonrpc":"2.0","method":"prompt","id":"second","params":{"user_input":"busy"}}`))
	host(t, &p, request("ApprovalRequest", "a", "tool", ""), registry.ActivityWaiting)
	host(t, &p, `{"jsonrpc":"2.0","id":"history","error":{"code":-32000,"message":"busy"}}`, "")
	host(t, &p, `{"jsonrpc":"2.0","id":"second","error":{"code":-32000,"message":"busy"}}`, "")
	host(t, &p, event("ApprovalResponse", `{"request_id":"a","response":"approve"}`), registry.ActivityRunning)
}

func TestMalformedTrackedMessagesFailWithoutPayloadDisclosure(t *testing.T) {
	for _, line := range []string{
		`{"jsonrpc":"2.0","method":"request","id":"a","params":{"type":"ApprovalRequest","payload":{"id":"a","tool_call_id":123,"description":"SECRET"}}}`,
		event("ApprovalResponse", `{"request_id":"a","response":"SECRET"}`),
		event("ToolResult", `{"tool_call_id":null,"return_value":"SECRET"}`),
		event("TurnEnd", `null`),
		`{"jsonrpc":"2.0","id":"turn","result":{"status":"SECRET"}}`,
		`{"jsonrpc":"2.0","id":"turn","error":{"code":"SECRET"}}`,
		`{"jsonrpc":"2.0","method":"event","params":`,
	} {
		var p kimi.Protocol
		begin(t, &p)
		host(t, &p, request("ApprovalRequest", "a", "tool", ""), registry.ActivityWaiting)
		_, changed, err := p.ObserveHost([]byte(line))
		if err == nil || changed || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("malformed tracked message: changed=%v error=%v", changed, err)
		}
		host(t, &p, event("ApprovalResponse", `{"request_id":"a","response":"reject"}`), registry.ActivityRunning)
	}
}

func TestUnknownExtensionAndUncorrelatedErrorDoNotChangeWaiting(t *testing.T) {
	var p kimi.Protocol
	begin(t, &p)
	host(t, &p, request("ApprovalRequest", "a", "tool", ""), registry.ActivityWaiting)
	host(t, &p, event("FutureExtension", `{"secret":"not retained"}`), "")
	host(t, &p, `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"bad client frame"}}`, "")
	host(t, &p, event("ApprovalResponse", `{"request_id":"a","response":"approve"}`), registry.ActivityRunning)
}

func TestPendingRequestLimitFailsInsteadOfLosingWaitingCorrelation(t *testing.T) {
	var p kimi.Protocol
	begin(t, &p)
	for i := range 1024 {
		host(t, &p, request("ApprovalRequest", fmt.Sprintf("approval-%d", i), fmt.Sprintf("tool-%d", i), ""), registry.ActivityWaiting)
	}
	if _, changed, err := p.ObserveHost([]byte(request("ApprovalRequest", "overflow", "overflow-tool", ""))); err == nil || changed {
		t.Fatalf("pending limit: changed=%v error=%v", changed, err)
	}
	host(t, &p, `{"jsonrpc":"2.0","id":"turn","result":{"status":"canceled"}}`, registry.ActivityInterrupted)
}
