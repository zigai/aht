package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

const titleRecordBytes = 64 << 10

var titleScanBuffers = sync.Pool{
	New: func() any { return new([2 * titleRecordBytes]byte) },
}

type titleScanState struct {
	Offset    int64  `json:"offset"`
	Manual    string `json:"manual"`
	Generated string `json:"generated"`
	Skipping  bool   `json:"skipping"`
}

func (claudeHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if identity.SessionID == "" || identity.SessionPath == "" {
			continue
		}
		title, err := readSessionTitle(ctx, identity.SessionID, identity.SessionPath)
		titles[i] = title
		if err != nil {
			failures = append(failures, fmt.Errorf("read Claude session title: %w", err))
		}
	}
	return titles, errors.Join(failures...)
}

func scanSessionTitle(ctx context.Context, reader io.Reader, sessionID string, state *titleScanState) (string, error) {
	buffer, ok := titleScanBuffers.Get().(*[2 * titleRecordBytes]byte)
	if !ok {
		panic("invalid Claude title scanner buffer type")
	}
	defer titleScanBuffers.Put(buffer)
	retained := 0
	offset := state.Offset
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("scan Claude session titles: %w", err)
		}
		count, readErr := reader.Read(buffer[retained:])
		offset += int64(count)
		chunk := buffer[:retained+count]
		if state.Skipping {
			end := bytes.IndexByte(chunk, '\n')
			if end < 0 {
				chunk = chunk[:0]
			} else {
				chunk = chunk[end+1:]
				state.Skipping = false
			}
		}
		end := bytes.LastIndexByte(chunk, '\n') + 1
		if err := scanTitleChunk(ctx, chunk[:end], sessionID, state); err != nil {
			return "", fmt.Errorf("scan Claude session titles: %w", err)
		}
		tail := chunk[end:]
		state.Offset = offset - int64(len(tail))
		if len(tail) > titleRecordBytes {
			state.Skipping = true
			state.Offset = offset
			retained = 0
		} else {
			retained = copy(buffer[:], tail)
		}
		if readErr == nil {
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			return "", fmt.Errorf("scan Claude session titles: %w", readErr)
		}
		return terminalSessionTitle(ctx, buffer[:retained], sessionID, state)
	}
}

func terminalSessionTitle(ctx context.Context, tail []byte, sessionID string, state *titleScanState) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("scan Claude session titles: %w", err)
	}
	terminal := *state
	if err := scanTitleChunk(ctx, tail, sessionID, &terminal); err != nil {
		return "", err
	}
	if terminal.Manual != "" {
		return terminal.Manual, nil
	}
	return terminal.Generated, nil
}

func scanTitleChunk(ctx context.Context, chunk []byte, sessionID string, state *titleScanState) error {
	for len(chunk) != 0 {
		hint := bytes.Index(chunk, []byte(`-title"`))
		if hint < 0 {
			break
		}
		start := bytes.LastIndexByte(chunk[:hint], '\n') + 1
		end := bytes.IndexByte(chunk[hint:], '\n')
		if end < 0 {
			end = len(chunk)
		} else {
			end += hint + 1
		}
		line := chunk[start:end]
		chunk = chunk[end:]
		if len(line) > titleRecordBytes ||
			(!bytes.Contains(line, []byte(`"custom-title"`)) && !bytes.Contains(line, []byte(`"ai-title"`))) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("scan Claude session titles: %w", err)
		}
		var record transcript.Record
		if json.Unmarshal(line, &record) != nil || transcript.Str(record, "sessionId") != sessionID {
			continue
		}
		kind, title, ok := titleRecord(record)
		if !ok {
			continue
		}
		if kind == "custom-title" {
			state.Manual = title
		} else {
			state.Generated = title
		}
	}
	return nil
}
