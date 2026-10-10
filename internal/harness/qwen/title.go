package qwen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/titlefile"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Qwen Code re-appends the current title near the end of the session file and
// reads it back from the same bounded tail and head windows.
const titleWindowBytes = 64 << 10

var titleRecordMarker = []byte(`"subtype":"custom_title"`)

//nolint:tagliatelle // Native Qwen Code session records use camelCase field names.
type titleRecordLine struct {
	Type          string `json:"type"`
	Subtype       string `json:"subtype"`
	SessionID     string `json:"sessionId"`
	SystemPayload struct {
		CustomTitle string `json:"customTitle"`
	} `json:"systemPayload"`
}

func (qwenHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		path := sessionPath(identity)
		if path == "" {
			continue
		}
		title, err := readSessionTitle(identity.SessionID, path)
		titles[i] = title
		if err != nil {
			failures = append(failures, fmt.Errorf("read Qwen Code session title: %w", err))
		}
	}
	return titles, errors.Join(failures...)
}

func sessionPath(identity registry.ObservationIdentity) string {
	if identity.SessionID == "" || filepath.Base(identity.SessionID) != identity.SessionID {
		return ""
	}
	if identity.SessionPath != "" {
		return identity.SessionPath
	}
	base := runtimeDirectory(harness.HomeDir())
	if base == "" || identity.CWD == "" {
		return ""
	}
	return filepath.Join(base, "projects", projectDirectoryName(identity.CWD), "chats", identity.SessionID+".jsonl")
}

func readSessionTitle(sessionID, path string) (string, error) {
	file, err := titlefile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open session: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect session: %w", err)
	}
	tailOffset := max(0, info.Size()-titleWindowBytes)
	title, err := scanTitleWindow(file, tailOffset, sessionID)
	if title != "" || tailOffset == 0 || err != nil {
		return title, err
	}
	return scanTitleWindow(file, 0, sessionID)
}

// scanTitleWindow returns the last title in the window starting at offset.
// Lines cut by the window edges fail to decode and are skipped.
func scanTitleWindow(file *os.File, offset int64, sessionID string) (string, error) {
	buffer := make([]byte, titleWindowBytes)
	count, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read session: %w", err)
	}
	title := ""
	for line := range bytes.SplitSeq(buffer[:count], []byte{'\n'}) {
		if !bytes.Contains(line, titleRecordMarker) {
			continue
		}
		var record titleRecordLine
		if json.Unmarshal(line, &record) != nil || record.Type != "system" || record.Subtype != "custom_title" || record.SessionID != sessionID {
			continue
		}
		if record.SystemPayload.CustomTitle != "" {
			title = record.SystemPayload.CustomTitle
		}
	}
	return title, nil
}
