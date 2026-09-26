package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func FuzzPayloadAdapters(f *testing.F) {
	for _, harness := range SupportedNames() {
		f.Add(harness, []byte(`{"session_id":"session","cwd":"/work","hook_event_name":"Stop","model":"model"}`))
	}
	f.Add("agy", []byte(`{"conversationId":"session","workspacePaths":["/work"],"toolCall":{"name":"ask_permission"}}`))
	f.Add("codex", []byte("not json"))
	f.Add("claude", []byte(`{"session_id":"s","cwd":"/w","hook_event_name":"Stop","background_tasks":[{"type":"subagent"}],"session_crons":[{"schedule":"*/5 1-3,7 */2 * 0-7"}]}`))

	f.Fuzz(func(t *testing.T, harnessName string, payload []byte) {
		harness, err := Normalize(harnessName)
		if err != nil {
			return
		}
		raw := json.RawMessage(payload)
		_ = PayloadCompatibleWithHarness(harness, raw)
		_, _ = DefaultsFromPayloadWithError(harness, raw)
		_, _ = ActivityFromPayload(harness, "Stop", registry.ActivityIdle, raw, time.Unix(0, 0))
	})
}
