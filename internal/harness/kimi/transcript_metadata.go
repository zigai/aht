package kimi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/titlefile"
	"github.com/zigai/aht/v2/internal/harness/transcript"
)

//nolint:tagliatelle // Native Kimi session state uses camelCase field names.
type kimiSessionState struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	TitleKind  string          `json:"titleKind"`
	LastPrompt string          `json:"lastPrompt"`
	CWD        string          `json:"cwd"`
	CreatedAt  json.RawMessage `json:"createdAt"`
	UpdatedAt  json.RawMessage `json:"updatedAt"`
}

func kimiTranscriptSessionDir(path string) string {
	if filepath.Base(path) != "wire.jsonl" || filepath.Base(filepath.Dir(path)) != "main" {
		return ""
	}
	agents := filepath.Dir(filepath.Dir(path))
	if filepath.Base(agents) != "agents" {
		return ""
	}
	return filepath.Dir(agents)
}

func readKimiSessionState(sessionDir, sessionID string) (kimiSessionState, error) {
	var state kimiSessionState
	path := filepath.Join(sessionDir, "state.json")
	file, err := titlefile.Open(path)
	if err != nil {
		return state, fmt.Errorf("open Kimi Code session state: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, transcript.MaxRecordBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return state, fmt.Errorf("read Kimi Code session state: %w", err)
	}
	if len(data) > transcript.MaxRecordBytes {
		return state, transcript.ErrRecordSize
	}
	if json.Unmarshal(data, &state) != nil {
		return state, transcript.ErrInvalidRecord
	}
	if state.ID == "" || state.ID != sessionID {
		return kimiSessionState{}, transcript.ErrUnknownFormat
	}
	return state, nil
}

func initializeTranscript(decoder *transcript.Decoder) {
	sessionDir := kimiTranscriptSessionDir(decoder.Conversation.Path)
	if sessionDir == "" {
		return
	}
	decoder.Conversation.SessionID = filepath.Base(sessionDir)
	state, err := readKimiSessionState(sessionDir, decoder.Conversation.SessionID)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		decoder.Issue(filepath.Join(sessionDir, "state.json"), err)
		return
	}
	decoder.Conversation.Title = state.Title
	switch state.TitleKind {
	case "custom":
		decoder.Conversation.CustomTitle = state.Title
	case "generated":
		decoder.Conversation.AITitle = state.Title
	}
	decoder.Conversation.CWD = state.CWD
	decoder.Conversation.CreatedAt = transcript.ParseTime(state.CreatedAt)
	decoder.Conversation.UpdatedAt = transcript.ParseTime(state.UpdatedAt)
}

func transcriptExtra(path string, _ map[string]string, stamp func(string) string) string {
	sessionDir := kimiTranscriptSessionDir(path)
	if sessionDir == "" {
		return ""
	}
	return stamp(filepath.Join(sessionDir, "state.json"))
}
