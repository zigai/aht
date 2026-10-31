//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var errOmpApprovalRequest = errors.New("unexpected OMP native approval request")

// OMP can offer session-scoped grants alongside the per-call Approve/Deny
// choices. Match the exact per-call labels, not their positions or the total
// option count, so this scenario never grants session-wide approval.
// https://github.com/can1357/oh-my-pi/blob/v18.12.6/docs/approval-mode.md#per-tool-prompt-details
// https://github.com/can1357/oh-my-pi/blob/v18.12.6/docs/rpc.md#extension-ui-sub-protocol
func ompPermissionResponse(request map[string]any, allow bool) (map[string]any, error) {
	id, _ := request["id"].(string)
	title, _ := request["title"].(string)

	options, _ := request["options"].([]any)
	if id == "" || request["type"] != "extension_ui_request" || request["method"] != "select" ||
		strings.SplitN(title, "\n", 2)[0] != "Allow tool: bash" {
		return nil, errOmpApprovalRequest
	}

	counts := make(map[string]int)

	for _, option := range options {
		label, ok := option.(string)
		if !ok {
			return nil, errOmpApprovalRequest
		}

		counts[label]++
	}

	if counts["Approve"] != 1 || counts["Deny"] != 1 {
		return nil, errOmpApprovalRequest
	}

	choice := "Deny"
	if allow {
		choice = "Approve"
	}

	return map[string]any{"type": "extension_ui_response", "id": id, "value": choice}, nil
}

func TestOmpPermissionResponse(t *testing.T) {
	for _, options := range []string{
		`["Approve","Deny"]`,
		`["Approve","Approve bash for this session","Deny"]`,
		`["Deny","Approve bash for this session","Approve"]`,
	} {
		for _, allow := range []bool{true, false} {
			want := "Deny"
			if allow {
				want = "Approve"
			}

			t.Run(options+"/"+want, func(t *testing.T) {
				var request map[string]any
				if err := json.Unmarshal(
					[]byte(
						`{"type":"extension_ui_request","method":"select",`+
							`"id":"native-id","title":"Allow tool: bash\nCommand: printf `+
							`aht-compat-marker","options":`+options+`}`,
					),
					&request,
				); err != nil {
					t.Fatal(err)
				}

				response, err := ompPermissionResponse(request, allow)
				if err != nil || response["id"] != "native-id" ||
					response["type"] != "extension_ui_response" ||
					response["value"] != want {
					t.Fatalf("response = %v, %v; want correlated %s", response, err, want)
				}
			})
		}
	}
}

func TestOmpPermissionResponseRejectsInvalidRequest(t *testing.T) {
	for _, patch := range []string{
		`{"id":""}`, `{"id":3}`, `{"method":"confirm"}`, `{"type":"other"}`,
		`{"title":"Allow tool: bash_other"}`, `{"title":"Allow tool: read"}`, `{"title":null}`,
		`{"options":null}`, `{"options":"Approve,Deny"}`, `{"options":["Approve"]}`,
		`{"options":["Approve bash for this session","Deny"]}`,
		`{"options":["Approve","Deny for this session"]}`,
		`{"options":["Approve","Approve","Deny"]}`,
		`{"options":["Approve","Deny","Deny"]}`,
		`{"options":["Approve","Deny",{}]}`,
	} {
		t.Run(patch, func(t *testing.T) {
			for _, allow := range []bool{true, false} {
				request := map[string]any{
					"type":    "extension_ui_request",
					"method":  "select",
					"id":      "native-id",
					"title":   "Allow tool: bash",
					"options": []any{"Approve", "Deny"},
				}
				if err := json.Unmarshal([]byte(patch), &request); err != nil {
					t.Fatal(err)
				}

				if response, err := ompPermissionResponse(
					request,
					allow,
				); response != nil ||
					!errors.Is(err, errOmpApprovalRequest) {
					t.Fatalf("invalid request answered: %v, %v", response, err)
				}
			}
		})
	}
}
