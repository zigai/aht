package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestReadPayloadInputAcceptsExactLimit(t *testing.T) {
	t.Parallel()

	data, err := readPayloadInput(strings.NewReader(strings.Repeat("x", maxPayloadInputBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != maxPayloadInputBytes {
		t.Fatalf("payload length = %d, want %d", len(data), maxPayloadInputBytes)
	}
}

func TestReadReportPayloadInputDropsLargeToolContent(t *testing.T) {
	t.Parallel()

	payload := `{"session_id":"session","tool_input":{"path":"/tmp/image.png"},"tool_response":"` +
		strings.Repeat("x", maxPayloadInputBytes) + `"}`
	data, err := readReportPayloadInput(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}

	var defaults map[string]json.RawMessage
	if err := json.Unmarshal(data, &defaults); err != nil {
		t.Fatalf("decoding retained defaults: %v", err)
	}
	if string(defaults["session_id"]) != `"session"` {
		t.Fatalf("session metadata = %s", defaults["session_id"])
	}
	if _, ok := defaults["tool_input"]; ok {
		t.Fatalf("tool_input was retained: %s", data)
	}
	if _, ok := defaults["tool_response"]; ok {
		t.Fatalf("tool_response was retained: %s", data)
	}
}

func TestReadReportPayloadInputRejectsOversizedMetadata(t *testing.T) {
	t.Parallel()

	payload := `{"session_id":"` + strings.Repeat("x", maxPayloadInputBytes) + `"}`
	_, err := readReportPayloadInput(strings.NewReader(payload))
	if !errors.Is(err, errPayloadInputTooLarge) {
		t.Fatalf("error = %v, want %v", err, errPayloadInputTooLarge)
	}
}
