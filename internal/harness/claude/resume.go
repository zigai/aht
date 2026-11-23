package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/zigai/aht/v2/internal/harness"
)

const (
	// Claude Code refuses to resume a session started in Code Mode unless the
	// experiment is enabled again through this variable.
	codeModeAssignment = "CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=1"
	// Claude writes its repl-mode record with the session metadata at the
	// start of the transcript, so the head is enough to find it.
	codeModeScanBytes = 64 << 10
)

var replModeMarker = []byte(`"repl-mode"`)

//nolint:tagliatelle // Claude Code's native transcript fields use camelCase.
type replModeRecord struct {
	Type     string `json:"type"`
	ReplMode bool   `json:"replMode"`
}

func (claudeHarness) ResumeCommand(sessionID string, sessionPath string) []string {
	if sessionID == "" {
		return nil
	}

	command := []string{claudeCommand, harness.ResumeFlag, sessionID}
	if startedInCodeMode(sessionPath) {
		return append([]string{"env", codeModeAssignment}, command...)
	}

	return command
}

func startedInCodeMode(sessionPath string) bool {
	if sessionPath == "" {
		return false
	}

	file, err := os.Open(sessionPath)
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	head, err := io.ReadAll(io.LimitReader(file, codeModeScanBytes))
	if err != nil {
		return false
	}

	if len(head) == codeModeScanBytes {
		if end := bytes.LastIndexByte(head, '\n'); end >= 0 {
			head = head[:end]
		}
	}

	codeMode := false

	for line := range bytes.Lines(head) {
		if !bytes.Contains(line, replModeMarker) {
			continue
		}

		var record replModeRecord

		if json.Unmarshal(line, &record) == nil && record.Type == "repl-mode" {
			codeMode = record.ReplMode
		}
	}

	return codeMode
}
