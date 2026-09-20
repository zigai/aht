package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	maxPayloadInputBytes         = 1 << 20
	maxDefaultsPayloadInputBytes = 64 << 20
)

var (
	errPayloadInputTooLarge         = errors.New("payload exceeds 1 MiB limit")
	errDefaultsPayloadInputTooLarge = errors.New("defaults payload exceeds 64 MiB limit")
)

func readPayloadInput(reader io.Reader) ([]byte, error) {
	return readPayloadInputWithLimit(reader, maxPayloadInputBytes, errPayloadInputTooLarge)
}

func readPayloadDefaultsInput(reader io.Reader) ([]byte, error) {
	data, err := readPayloadInputWithLimit(
		reader,
		maxDefaultsPayloadInputBytes,
		errDefaultsPayloadInputTooLarge,
	)
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return retainBoundedPayloadDefaults(data)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return retainBoundedPayloadDefaults(data)
	}
	delete(payload, "tool_input")
	delete(payload, "tool_response")
	delete(payload, "toolInput")
	delete(payload, "toolResponse")

	data, err = json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding defaults payload: %w", err)
	}
	return retainBoundedPayloadDefaults(data)
}

func retainBoundedPayloadDefaults(data []byte) ([]byte, error) {
	if len(data) > maxPayloadInputBytes {
		return nil, errPayloadInputTooLarge
	}
	return data, nil
}

func readPayloadInputWithLimit(reader io.Reader, limit int, limitErr error) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("reading payload input: %w", err)
	}
	if len(data) > limit {
		return nil, limitErr
	}
	return data, nil
}

func normalizeRawPayloadBytes(data []byte) (json.RawMessage, error) {
	data = bytes.Clone(bytes.TrimSpace(data))
	if len(data) == 0 {
		return nil, nil
	}
	if json.Valid(data) {
		return json.RawMessage(data), nil
	}
	wrapped, err := json.Marshal(string(data))
	if err != nil {
		return nil, fmt.Errorf("encode raw payload: %w", err)
	}
	return json.RawMessage(wrapped), nil
}
