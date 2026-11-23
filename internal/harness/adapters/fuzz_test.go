package adapters

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func FuzzPayloadAdapters(f *testing.F) {
	f.Setenv("HOME", f.TempDir())
	f.Setenv("KIMI_CODE_HOME", f.TempDir())

	for _, id := range Harnesses() {
		harnessID := string(id)
		f.Add(
			harnessID,
			[]byte(
				`{"session_id":"session","cwd":"/work","hook_event_name":"Stop","model":"model"}`,
			),
		)
		f.Add(
			harnessID,
			[]byte(
				`{"session_id":"snake","sessionId":"camel",`+
					`"conversation_id":"conversation-snake",`+
					`"conversationId":"conversation-camel","taskId":"task",`+
					`"sessionContext":{"rootSessionId":"root"},"cwd":"/work",`+
					`"hook_event_name":"Stop"}`,
			),
		)
		f.Add(
			harnessID,
			[]byte(
				`{"session_id":" padded ","sessionId":"padded",`+
					`"taskId":"padded","sessionContext":{"rootSessionId":"\tpadde`+
					`d\n"},"cwd":"/work"}`,
			),
		)
		f.Add(
			harnessID,
			[]byte(
				`{"session_id":null,"sessionId":4,"conversation_id":[],`+
					`"taskId":{},"sessionContext":{"rootSessionId":false},`+
					`"prompt":"unrelated-identity"}`,
			),
		)
		f.Add(harnessID, []byte(`null`))
		f.Add(harnessID, []byte(`["session"]`))
	}

	f.Add(
		"agy",
		[]byte(
			`{"conversationId":"session","workspacePaths":["/work"],"toolCall":{"name":"ask_permission"}}`,
		),
	)
	f.Add("codex", []byte("not json"))
	f.Add(
		"claude",
		[]byte(
			`{"session_id":"s","cwd":"/w","hook_event_name":"Stop",`+
				`"background_tasks":[{"type":"subagent"}],`+
				`"session_crons":[{"schedule":"*/5 1-3,7 */2 * 0-7"}]}`,
		),
	)

	f.Fuzz(func(t *testing.T, harnessName string, payload []byte) {
		harnessID, err := Parse(harnessName)
		if err != nil {
			return
		}

		raw := json.RawMessage(payload)
		checkPayloadCompatibility(t, harnessID, raw)
		checkPayloadIdentity(t, harnessID, raw)

		activity, activityErr := ActivityFromPayload(
			harnessID,
			"Stop",
			registry.ActivityIdle,
			raw,
			time.Unix(0, 0),
		)
		if !activity.Valid() || (activityErr != nil && activity != registry.ActivityIdle) {
			t.Fatalf("%s activity = %q, error = %v", harnessID, activity, activityErr)
		}
	})
}

func checkPayloadCompatibility(t *testing.T, harnessID registry.Harness, payload json.RawMessage) {
	t.Helper()

	adapter, found := Find(harnessID)
	if !found {
		t.Fatalf("parsed harness %s has no adapter", harnessID)
	}

	if _, native := adapter.(harness.PayloadAdapter); !native || len(payload) == 0 ||
		!PayloadCompatible(harnessID, payload) {
		return
	}

	if _, object := harness.PayloadObject(payload); !object {
		t.Fatalf("%s accepted a non-object payload: %s", harnessID, payload)
	}
}

func checkPayloadIdentity(t *testing.T, harnessID registry.Harness, payload json.RawMessage) {
	t.Helper()

	defaults, defaultsErr := PayloadDefaults(harnessID, payload)

	document, object := harness.PayloadObject(payload)
	if !object {
		return
	}

	if defaultsErr != nil {
		t.Fatalf("%s rejected an object payload: %v", harnessID, defaultsErr)
	}

	if defaults.SessionID == "" {
		return
	}

	if !containsPayloadString(document, defaults.SessionID) {
		t.Fatalf("%s invented identity %q absent from %s", harnessID, defaults.SessionID, payload)
	}

	withoutIdentity, err := json.Marshal(withoutPayloadString(document, defaults.SessionID))
	if err != nil {
		t.Fatal(err)
	}

	remaining, err := PayloadDefaults(harnessID, withoutIdentity)
	if err != nil {
		t.Fatalf("%s rejected a payload without its identity: %v", harnessID, err)
	}

	if remaining.SessionID == defaults.SessionID {
		t.Fatalf(
			"%s kept identity %q after every payload value carrying it was removed: %s",
			harnessID,
			defaults.SessionID,
			withoutIdentity,
		)
	}
}

func containsPayloadString(value any, target string) bool {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value) == target
	case map[string]any:
		for _, child := range value {
			if containsPayloadString(child, target) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsPayloadString(child, target) {
				return true
			}
		}
	}

	return false
}

func withoutPayloadString(value any, target string) any {
	switch value := value.(type) {
	case string:
		if strings.TrimSpace(value) == target {
			return nil
		}

		return value
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = withoutPayloadString(child, target)
		}

		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = withoutPayloadString(child, target)
		}

		return result
	default:
		return value
	}
}
