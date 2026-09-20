package history

import (
	"crypto/md5" //nolint:gosec // G501: Kimi's native directory mapping mandates MD5; it is metadata lookup, not authentication. TestKimiNativeDirectoryMetadata covers compatibility; Kimi owns the format.
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func (s *search) kimiMetadata(source Source, sessionsDir string) map[string]string {
	// Native Kimi metadata maps paths to MD5 directory names. Never try to
	// reverse directory encodings or infer cwd from a session filename.
	rootPath := filepath.Dir(sessionsDir)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		s.issue(source, rootPath, err)
		return nil
	}
	defer func() {
		if err := root.Close(); err != nil {
			s.issue(source, rootPath, err)
		}
	}()
	file, err := root.Open("kimi.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.issue(source, rootPath, err)
		return nil
	}
	defer func() {
		if err := file.Close(); err != nil {
			s.issue(source, rootPath, err)
		}
	}()
	var metadata struct {
		WorkDirs []struct {
			Path string `json:"path"`
			Kaos string `json:"kaos"`
		} `json:"work_dirs"`
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		s.issue(source, rootPath, err)
		return nil
	}
	if len(data) > maxRecordBytes {
		s.issue(source, rootPath, errRecordSize)
		return nil
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		s.issue(source, rootPath, errInvalidRecord)
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
