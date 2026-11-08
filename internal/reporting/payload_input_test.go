package reporting

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestReadPayloadInputAcceptsExactLimit(t *testing.T) {
	t.Parallel()

	data, err := readReportPayloadInput(
		strings.NewReader(strings.Repeat("x", maxPayloadInputBytes)),
		"claude",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) != maxPayloadInputBytes {
		t.Fatalf("payload length = %d, want %d", len(data), maxPayloadInputBytes)
	}
}

func TestReadReportPayloadInputDropsLargeToolContent(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                                  string
		harness                               registry.Harness
		sessionField, inputField, outputField string
	}{
		{"command hooks", "claude", "session_id", "tool_input", "tool_response"},
		{"native tool hooks", "copilot", "sessionId", "toolArgs", "toolResult"},
		{"editor tool hooks", "copilot", "session_id", "tool_input", "tool_result"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			payload := fmt.Sprintf(
				`{%q:"session",%q:{"path":"sample.png"},%q:%q}`,
				test.sessionField,
				test.inputField,
				test.outputField,
				strings.Repeat("x", maxPayloadInputBytes),
			)

			data, err := readReportPayloadInput(strings.NewReader(payload), test.harness)
			if err != nil {
				t.Fatal(err)
			}

			var defaults map[string]json.RawMessage
			if err := json.Unmarshal(data, &defaults); err != nil {
				t.Fatalf("decoding retained defaults: %v", err)
			}

			if string(defaults[test.sessionField]) != `"session"` {
				t.Fatalf("session metadata = %s", defaults[test.sessionField])
			}

			for _, field := range []string{test.inputField, test.outputField} {
				if _, ok := defaults[field]; ok {
					t.Fatalf("tool content was retained in %s", field)
				}
			}
		})
	}
}

func TestReadReportPayloadInputRejectsOversizedMetadata(t *testing.T) {
	t.Parallel()

	payload := `{"session_id":"` + strings.Repeat("x", maxPayloadInputBytes) + `"}`

	_, err := readReportPayloadInput(strings.NewReader(payload), "claude")
	if !errors.Is(err, errPayloadInputTooLarge) {
		t.Fatalf("error = %v, want %v", err, errPayloadInputTooLarge)
	}
}
