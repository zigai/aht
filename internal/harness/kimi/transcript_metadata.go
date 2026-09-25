package kimi

import (
	"crypto/md5" //nolint:gosec // G501: Kimi's native directory mapping mandates MD5; it is metadata lookup, not authentication. TestKimiNativeDirectoryMetadata covers compatibility; Kimi owns the format.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

type kimiWorkDir struct {
	Path string `json:"path"`
	Kaos string `json:"kaos"`
}

type kimiMetadata struct {
	WorkDirs []kimiWorkDir `json:"work_dirs"`
}

func transcriptMetadata(sessionsDir string, issue func(string, error)) map[string]string {
	// Native Kimi metadata maps paths to MD5 directory names. Never try to
	// reverse directory encodings or infer cwd from a session filename.
	rootPath := filepath.Dir(sessionsDir)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		issue(rootPath, err)
		return nil
	}
	defer func() {
		if err := root.Close(); err != nil {
			issue(rootPath, err)
		}
	}()
	file, err := root.Open("kimi.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		issue(rootPath, err)
		return nil
	}
	defer func() {
		if err := file.Close(); err != nil {
			issue(rootPath, err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, transcript.MaxRecordBytes+1))
	if err != nil {
		issue(rootPath, err)
		return nil
	}
	if len(data) > transcript.MaxRecordBytes {
		issue(rootPath, transcript.ErrRecordSize)
		return nil
	}
	metadata, err := decodeWorkspaceMetadata(data)
	if err != nil {
		issue(rootPath, transcript.ErrInvalidRecord)
		return nil
	}
	dirs := make(map[string]string, len(metadata.WorkDirs))
	for _, entry := range metadata.WorkDirs {
		digest := md5.Sum([]byte(entry.Path)) //nolint:gosec // G401: Kimi owns this non-security MD5 path encoding; TestKimiNativeDirectoryMetadata covers compatibility. No identity or authorization depends on this digest.
		name := hex.EncodeToString(digest[:])
		if entry.Kaos != "" && entry.Kaos != "local" {
			name = entry.Kaos + "_" + name
		}
		dirs[name] = entry.Path
	}
	return dirs
}

func transcriptSourceMetadata(path string, isDir bool, issue func(string, error)) map[string]string {
	if isDir {
		return transcriptMetadata(path, issue)
	}
	sessionsDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if filepath.Base(sessionsDir) == "sessions" {
		return transcriptMetadata(sessionsDir, issue)
	}
	return nil
}

func decodeWorkspaceMetadata(data []byte) (kimiMetadata, error) {
	var metadata kimiMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, fmt.Errorf("decode workspace metadata: %w", err)
	}
	return metadata, nil
}
