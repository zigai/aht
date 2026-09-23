package cline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

const (
	maxHistoryOutputBytes = 16 << 20
	maxHistoryEntries     = 2000
	clineTitleTimeout     = 5 * time.Second
)

var errClineHistoryOutputTooLarge = errors.New("cline history output exceeds 16 MiB")

type clineHistoryMetadata struct {
	Title string `json:"title"`
}

type clineHistoryEntry struct {
	SessionID string               `json:"-"`
	Metadata  clineHistoryMetadata `json:"metadata"`
}

type clineHistoryOutput struct {
	bytes.Buffer

	exceeded bool
}

func (entry *clineHistoryEntry) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode Cline history entry: %w", err)
	}
	if raw, ok := fields["sessionId"]; ok {
		if err := json.Unmarshal(raw, &entry.SessionID); err != nil {
			return fmt.Errorf("decode Cline session identifier: %w", err)
		}
	}
	if raw, ok := fields["metadata"]; ok {
		if err := json.Unmarshal(raw, &entry.Metadata); err != nil {
			return fmt.Errorf("decode Cline history metadata: %w", err)
		}
	}
	return nil
}

func (output *clineHistoryOutput) Write(data []byte) (int, error) {
	if len(data) > maxHistoryOutputBytes-output.Len() {
		output.exceeded = true
		return 0, errClineHistoryOutputTooLarge
	}
	n, err := output.Buffer.Write(data)
	if err != nil {
		return n, fmt.Errorf("write Cline history output: %w", err)
	}
	return n, nil
}

func (clineHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Cline session titles: %w", err)
	}
	if len(identities) == 0 {
		return titles, nil
	}
	entries, apiErr := listClineSessionTitles(ctx)
	var failures []error
	if apiErr != nil {
		failures = append(failures, apiErr)
	}
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if identity.SessionID == "" {
			continue
		}
		if entry, ok := entries[identity.SessionID]; ok {
			titles[i] = strings.TrimSpace(entry.Metadata.Title)
		}
	}
	return titles, errors.Join(failures...)
}

func listClineSessionTitles(ctx context.Context) (map[string]clineHistoryEntry, error) {
	entries := make(map[string]clineHistoryEntry)
	binary, err := exec.LookPath(clineCommand)
	if err != nil {
		return entries, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, clineTitleTimeout)
	defer cancel()
	command := exec.CommandContext(requestCtx, binary, "history", "--json", "--limit", strconv.Itoa(maxHistoryEntries))
	var output clineHistoryOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return entries, fmt.Errorf("list Cline session history: %w", ctx.Err())
		}
		if requestCtx.Err() != nil {
			return entries, fmt.Errorf("list Cline session history: %w", requestCtx.Err())
		}
		if output.exceeded {
			return entries, errClineHistoryOutputTooLarge
		}
		return entries, fmt.Errorf("list Cline session history: %w", err)
	}
	var history []clineHistoryEntry
	if err := json.Unmarshal(output.Bytes(), &history); err != nil {
		return entries, fmt.Errorf("decode Cline session history: %w", err)
	}
	for _, entry := range history {
		if entry.SessionID != "" {
			entries[entry.SessionID] = entry
		}
	}
	return entries, nil
}
