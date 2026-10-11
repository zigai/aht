package claude

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	titleCacheVersion      = 2
	titleCacheStringFields = 3
	titleJSONEscapeBytes   = 6
)

type titleSourceStamp struct {
	Device string `json:"device"`
	Inode  uint64 `json:"inode"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Ctime  int64  `json:"ctime"`
}

type titleCacheRecord struct {
	Version   int              `json:"version"`
	Path      string           `json:"path"`
	SessionID string           `json:"session_id"`
	Stamp     titleSourceStamp `json:"stamp"`
	State     *titleScanState  `json:"state"`
	Title     *string          `json:"title"`
}

func readSessionTitle(ctx context.Context, sessionID, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read Claude session title: %w", err)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Claude transcript: %w", err)
	}
	file, info, err := openTitleSource(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("read Claude session title: %w", err)
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open Claude transcript: %w", err)
	}
	title, readErr := readTitleSnapshot(ctx, file, info, path, sessionID)
	if err := file.Close(); err != nil {
		return "", errors.Join(readErr, fmt.Errorf("close Claude transcript: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return "", errors.Join(readErr, fmt.Errorf("read Claude session title: %w", err))
	}
	return title, readErr
}

func readTitleSnapshot(ctx context.Context, file *os.File, info os.FileInfo, path, sessionID string) (string, error) {
	stamp, err := stampTitleSource(info)
	if err != nil {
		return "", err
	}
	cachePath, cacheErr := titleCachePath(path, sessionID)
	var cached titleCacheRecord
	var valid bool
	if cacheErr == nil {
		cached, valid, cacheErr = loadTitleCache(cachePath, path, sessionID)
	}
	if err := ctx.Err(); err != nil {
		return "", errors.Join(cacheErr, err)
	}
	var state titleScanState
	if valid {
		switch {
		case cached.Stamp == stamp:
			return *cached.Title, nil
		case cached.Stamp.Device == stamp.Device && cached.Stamp.Inode == stamp.Inode && stamp.Size > cached.Stamp.Size:
			state = *cached.State
		}
	}
	title, stable, err := scanTitleSnapshot(ctx, file, path, sessionID, stamp, &state)
	if err != nil {
		return title, errors.Join(cacheErr, err)
	}
	if !stable {
		return title, cacheErr
	}
	if cachePath == "" {
		return title, cacheErr
	}
	record := titleCacheRecord{Version: titleCacheVersion, Path: path, SessionID: sessionID, Stamp: stamp, State: &state, Title: &title}
	return title, errors.Join(cacheErr, publishTitleCache(ctx, cachePath, record))
}

func scanTitleSnapshot(ctx context.Context, file *os.File, path, sessionID string, stamp titleSourceStamp, state *titleScanState) (string, bool, error) {
	if _, err := file.Seek(state.Offset, io.SeekStart); err != nil {
		return "", false, fmt.Errorf("seek Claude transcript: %w", err)
	}
	title, err := scanSessionTitle(ctx, io.LimitReader(file, stamp.Size-state.Offset), sessionID, state)
	if err != nil {
		return "", false, err
	}
	opened, err := file.Stat()
	if err != nil {
		return "", false, fmt.Errorf("inspect Claude transcript after scan: %w", err)
	}
	after, err := stampTitleSource(opened)
	if err != nil {
		return "", false, err
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return title, false, nil
	}
	if err != nil {
		return title, false, fmt.Errorf("inspect Claude transcript after scan: %w", err)
	}
	stable := after == stamp && current.Mode().IsRegular() && os.SameFile(current, opened)
	return title, stable, nil
}

func titleCachePath(path, sessionID string) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve Claude title cache: %w", err)
	}
	dir := filepath.Join(root, "aht", "claude-titles")
	for _, directory := range []string{filepath.Join(root, "aht"), dir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", fmt.Errorf("create Claude title cache directory: %w", err)
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return "", fmt.Errorf("inspect Claude title cache directory: %w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("inspect Claude title cache directory %q: %w", directory, os.ErrInvalid)
		}
		if info.Mode().Perm() != 0o700 {
			if err := os.Chmod(directory, 0o700); err != nil {
				return "", fmt.Errorf("protect Claude title cache directory: %w", err)
			}
		}
	}
	key := sha256.Sum256([]byte(path + "\x00" + sessionID))
	return filepath.Join(dir, fmt.Sprintf("%x.json", key)), nil
}

func loadTitleCache(path, source, sessionID string) (titleCacheRecord, bool, error) {
	var record titleCacheRecord
	file, _, err := openTitleSource(path)
	if errors.Is(err, os.ErrNotExist) {
		return record, false, nil
	}
	if err != nil {
		return record, false, fmt.Errorf("open Claude title cache: %w", err)
	}
	maxBytes := int64(titleJSONEscapeBytes*(titleCacheStringFields*titleRecordBytes+len(source)+len(sessionID)) + titleRecordBytes)
	body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return record, false, fmt.Errorf("read Claude title cache: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return record, false, nil
	}
	record, valid := decodeTitleCache(body, source, sessionID)
	return record, valid, nil
}

func decodeTitleCache(body []byte, path, sessionID string) (titleCacheRecord, bool) {
	var record titleCacheRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return record, false
	}
	return record, record.matchesSource(path, sessionID)
}

func (record *titleCacheRecord) matchesSource(path, sessionID string) bool {
	return record.Version == titleCacheVersion && record.Path == path && record.SessionID == sessionID &&
		record.Stamp.Inode != 0 && record.Stamp.Size >= 0 && record.State != nil && record.Title != nil &&
		record.State.validCheckpoint(record.Stamp.Size)
}

func (state *titleScanState) validCheckpoint(size int64) bool {
	return state.Offset >= 0 && state.Offset <= size && (!state.Skipping || state.Offset == size)
}

func publishTitleCache(ctx context.Context, path string, record titleCacheRecord) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish Claude title cache: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".claude-title-*")
	if err != nil {
		return fmt.Errorf("create Claude title cache snapshot: %w", err)
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	writeErr := temp.Chmod(0o600)
	if writeErr == nil {
		writeErr = json.NewEncoder(temp).Encode(record)
	}
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return fmt.Errorf("write Claude title cache snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish Claude title cache: %w", err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("publish Claude title cache snapshot: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open Claude title cache directory: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("sync Claude title cache directory: %w", err)
	}
	return nil
}
