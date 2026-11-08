package reporting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	maxPayloadInputBytes       = 1 << 20
	maxReportPayloadInputBytes = 64 << 20
)

var (
	errPayloadInputTooLarge       = errors.New("payload exceeds 1 MiB limit")
	errReportPayloadInputTooLarge = errors.New("report payload exceeds 64 MiB limit")
)

func readReportPayloadInput(reader io.Reader, harnessID registry.Harness) ([]byte, error) {
	data, err := readPayloadInputWithLimit(
		reader,
		maxReportPayloadInputBytes,
		errReportPayloadInputTooLarge,
	)
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return retainBoundedReportPayload(data)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return retainBoundedReportPayload(data)
	}

	for _, key := range catalog.PayloadOmissions(harnessID) {
		delete(payload, key)
	}

	data, err = json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding report payload: %w", err)
	}

	return retainBoundedReportPayload(data)
}

func retainBoundedReportPayload(data []byte) ([]byte, error) {
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
