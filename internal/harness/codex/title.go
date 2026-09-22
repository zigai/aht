package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zigai/aht/internal/harness/titlefile"
	"github.com/zigai/aht/pkg/registry"
)

// SessionTitles resolves native Codex names from its index and state database.
func (codexHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	byHome := make(map[string]map[string][]int)
	for i, identity := range identities {
		if identity.SessionID == "" {
			continue
		}
		home := titleHome(identity.SessionPath)
		if byHome[home] == nil {
			byHome[home] = make(map[string][]int)
		}
		byHome[home][identity.SessionID] = append(byHome[home][identity.SessionID], i)
	}
	var failures []error
	for home, indices := range byHome {
		if err := readTitleIndex(ctx, filepath.Join(home, "session_index.jsonl"), indices, titles); err != nil {
			failures = append(failures, fmt.Errorf("read Codex title index: %w", err))
		}
		if err := readStateTitles(ctx, home, indices, titles); err != nil {
			failures = append(failures, fmt.Errorf("read Codex state titles: %w", err))
		}
	}
	return titles, errors.Join(failures...)
}

func titleHome(sessionPath string) string {
	if sessionPath != "" {
		for dir := filepath.Dir(sessionPath); ; dir = filepath.Dir(dir) {
			if name := filepath.Base(dir); name == "sessions" || name == "archived_sessions" {
				return filepath.Dir(dir)
			}
			if parent := filepath.Dir(dir); parent == dir || dir == "." {
				break
			}
		}
	}
	return codexHome()
}

func readTitleIndex(ctx context.Context, path string, indices map[string][]int, titles []string) error {
	file, err := titlefile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open Codex title index: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only close cannot change the observed names.

	reader := bufio.NewReader(file)
	for {
		line, err := titlefile.ReadLine(ctx, reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, titlefile.ErrRecordTooLarge) {
			continue
		}
		if err != nil {
			return fmt.Errorf("scan Codex title index: %w", err)
		}
		if !bytes.Contains(line, []byte(`"thread_name"`)) {
			continue
		}
		var entry struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		for _, i := range indices[entry.ID] {
			titles[i] = entry.Name
		}
	}
}
