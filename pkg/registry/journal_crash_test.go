package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadSnapshotFileMissingPreservesPathError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	_, err := readSnapshotFile(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read missing snapshot: %v, want os.ErrNotExist", err)
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != path {
		t.Fatalf("read missing snapshot: %v, want PathError for %s", err, path)
	}
	if strings.Count(err.Error(), path) != 1 {
		t.Fatalf("read missing snapshot repeats path: %v", err)
	}
}

type crashFile struct {
	live   []byte
	stable []byte
}

type crashFS struct {
	dir       string
	live      map[string]*crashFile
	durable   map[string]*crashFile
	captures  []crashCapture
	nameIndex int
}

type crashCapture struct {
	boundary string
	images   []map[string][]byte
}

func newCrashFS(dir string) *crashFS {
	return &crashFS{dir: dir, live: make(map[string]*crashFile), durable: make(map[string]*crashFile)}
}

func (m *crashFS) Open(path string) (registryFile, error) {
	if path == m.dir {
		return &crashHandle{fs: m, name: path, dir: true}, nil
	}
	file, ok := m.live[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &crashHandle{fs: m, name: path, file: file}, nil
}

func (m *crashFS) OpenFile(path string, flag int, _ os.FileMode) (registryFile, error) {
	file, ok := m.live[path]
	if !ok {
		if flag&os.O_CREATE == 0 {
			return nil, os.ErrNotExist
		}
		file = &crashFile{}
		m.live[path] = file
		m.capture("create")
	}
	if flag&os.O_TRUNC != 0 {
		file.live = nil
		m.capture("truncate")
	}
	h := &crashHandle{fs: m, name: path, file: file}
	if flag&os.O_APPEND != 0 {
		h.append = true
		h.pos = int64(len(file.live))
	}
	return h, nil
}

func (m *crashFS) CreateTemp(dir, pattern string) (registryFile, error) {
	m.nameIndex++
	path := filepath.Join(dir, fmt.Sprintf("%s%d", pattern, m.nameIndex))
	file := &crashFile{}
	m.live[path] = file
	m.capture("create-temp")
	return &crashHandle{fs: m, name: path, file: file}, nil
}

func (m *crashFS) Rename(oldPath, newPath string) error {
	file, ok := m.live[oldPath]
	if !ok {
		return os.ErrNotExist
	}
	delete(m.live, oldPath)
	m.live[newPath] = file
	m.capture("rename")
	return nil
}

func (m *crashFS) Remove(path string) error {
	delete(m.live, path)
	m.capture("remove")
	return nil
}

func (m *crashFS) MkdirAll(string, os.FileMode) error { return nil }

func (m *crashFS) capture(boundary string) {
	// A crash may retain either directory image. File sync fixes contents, while an
	// unsynced write may retain any prefix; the selected prefixes cover the
	// journal parse outcomes because encoded entries contain no literal newlines.
	images := make([]map[string][]byte, 0, 10)
	for _, names := range []map[string]*crashFile{m.durable, m.live} {
		for mode := range 5 {
			image := make(map[string][]byte, len(names))
			for path, file := range names {
				common := crashCommonPrefix(file)
				pending := len(file.live) - common
				prefix := pending
				switch mode {
				case 0:
					prefix = -1
				case 1:
					prefix = 0
				case 2:
					prefix = pending / 2
				case 3:
					prefix = max(0, pending-1)
				}
				image[path] = crashBytes(file, common, prefix)
			}
			images = append(images, image)
		}
	}
	m.captures = append(m.captures, crashCapture{boundary: boundary, images: images})
}

func crashCommonPrefix(file *crashFile) int {
	common := 0
	for common < min(len(file.stable), len(file.live)) && file.stable[common] == file.live[common] {
		common++
	}
	return common
}

func crashBytes(file *crashFile, common, prefix int) []byte {
	if prefix < 0 {
		return bytes.Clone(file.stable)
	}
	return bytes.Clone(file.live[:common+min(prefix, len(file.live)-common)])
}

type crashHandle struct {
	fs     *crashFS
	name   string
	file   *crashFile
	pos    int64
	dir    bool
	append bool
}

func (h *crashHandle) Read(p []byte) (int, error) {
	if h.dir {
		return 0, io.EOF
	}
	if h.pos >= int64(len(h.file.live)) {
		return 0, io.EOF
	}
	n := copy(p, h.file.live[h.pos:])
	h.pos += int64(n)
	return n, nil
}

func (h *crashHandle) ReadAt(p []byte, off int64) (int, error) {
	if h.dir || off >= int64(len(h.file.live)) {
		return 0, io.EOF
	}
	n := copy(p, h.file.live[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (h *crashHandle) Write(p []byte) (int, error) {
	if h.append {
		h.pos = int64(len(h.file.live))
	}
	end := int(h.pos) + len(p)
	if end > len(h.file.live) {
		h.file.live = append(h.file.live, make([]byte, end-len(h.file.live))...)
	}
	copy(h.file.live[h.pos:], p)
	h.pos = int64(end)
	h.fs.capture("write")
	return len(p), nil
}
func (h *crashHandle) Close() error            { return nil }
func (h *crashHandle) Chmod(os.FileMode) error { return nil }
func (h *crashHandle) Name() string            { return h.name }
func (h *crashHandle) Stat() (os.FileInfo, error) {
	if h.dir {
		return crashInfo{size: 0}, nil
	}
	return crashInfo{size: int64(len(h.file.live))}, nil
}

func (h *crashHandle) Sync() error {
	if h.dir {
		h.fs.durable = make(map[string]*crashFile, len(h.fs.live))
		maps.Copy(h.fs.durable, h.fs.live)
		h.fs.capture("dir-sync")
		return nil
	}
	h.file.stable = bytes.Clone(h.file.live)
	h.fs.capture("file-sync")
	return nil
}

func (h *crashHandle) Truncate(size int64) error {
	if size < 0 {
		return os.ErrInvalid
	}
	if int(size) < len(h.file.live) {
		h.file.live = h.file.live[:size]
	} else if int(size) > len(h.file.live) {
		h.file.live = append(h.file.live, make([]byte, int(size)-len(h.file.live))...)
	}
	h.fs.capture("truncate")
	return nil
}

type crashInfo struct{ size int64 }

func (i crashInfo) Name() string       { return "state" }
func (i crashInfo) Size() int64        { return i.size }
func (i crashInfo) Mode() fs.FileMode  { return 0o600 }
func (i crashInfo) ModTime() time.Time { return time.Time{} }
func (i crashInfo) IsDir() bool        { return false }
func (i crashInfo) Sys() any           { return nil }

func crashFSFromImage(dir string, image map[string][]byte) *crashFS {
	m := newCrashFS(dir)
	for path, data := range image {
		file := &crashFile{live: bytes.Clone(data), stable: bytes.Clone(data)}
		m.live[path], m.durable[path] = file, file
	}
	return m
}

type crashOperation struct {
	name  string
	entry journalEntry
}

func TestJournalCrashPoints(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ops := []crashOperation{
		{"observe", journalEntry{ReceivedAt: base, Observations: []Observation{{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: "one"}, Evidence: &Report{Activity: new(ActivityRunning)}}}}},
		{"batch", journalEntry{ReceivedAt: base.Add(time.Second), Observations: []Observation{{Harness: HarnessPi, At: base.Add(time.Second), Subject: ObservationIdentity{SessionID: "two"}, Evidence: &Report{Activity: new(ActivityWaiting)}}, {Harness: HarnessPi, At: base.Add(time.Second), Subject: ObservationIdentity{SessionID: "three"}, Evidence: &Report{Activity: new(ActivityRunning)}}}}},
		{"gc", journalEntry{ReceivedAt: base.Add(2 * time.Second), DeleteAfter: new(time.Duration)}},
		{"reset", journalEntry{ReceivedAt: base.Add(3 * time.Second), Reset: true}},
	}
	if !testing.Short() {
		ops = append(ops, crashOperation{"after-reset", journalEntry{ReceivedAt: base.Add(4 * time.Second), Observations: []Observation{{Harness: HarnessPi, At: base.Add(4 * time.Second), Subject: ObservationIdentity{SessionID: "four"}, Evidence: &Report{Activity: new(ActivityRunning)}}}}})
	}
	for _, mode := range []string{"append", "snapshot", "mixed"} {
		t.Run(mode, func(t *testing.T) { testJournalCrashSequence(t, mode, ops) })
	}
}

type crashScenario struct {
	t       *testing.T
	dir     string
	path    string
	mem     *crashFS
	store   *Journal
	model   snapshot
	checked int
}

func testJournalCrashSequence(t *testing.T, mode string, ops []crashOperation) {
	t.Helper()
	dir := t.TempDir()
	scenario := &crashScenario{t: t, dir: dir, path: filepath.Join(dir, "state.json"), mem: newCrashFS(dir), model: newSnapshot()}
	scenario.store = NewJournal(scenario.path, fixtureRules{})
	registryFSByDir.Store(dir, scenario.mem)
	t.Cleanup(func() { registryFSByDir.Delete(dir) })
	if mode == "append" {
		owner, err := tryStoreLock(scenario.path + ".owner.lock")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	for index, op := range ops {
		if mode == "mixed" && index%2 == 0 {
			owner, err := tryStoreLock(scenario.path + ".owner.lock")
			if err != nil {
				t.Fatal(err)
			}
			scenario.runOperation(index, op)
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		scenario.runOperation(index, op)
	}
	if scenario.checked == 0 {
		t.Fatal("no crash points checked")
	}
}

func (s *crashScenario) runOperation(index int, op crashOperation) {
	s.t.Helper()
	start := len(s.mem.captures)
	s.store.setNowForTest(func() time.Time { return op.entry.ReceivedAt })
	var err error
	switch op.name {
	case "observe", "after-reset":
		_, err = s.store.Observe(s.t.Context(), op.entry.Observations[0])
	case "batch":
		_, err = s.store.ObserveBatch(s.t.Context(), op.entry.Observations)
	case "gc":
		_, err = s.store.GC(s.t.Context(), *op.entry.DeleteAfter)
	case "reset":
		_, err = s.store.Reset(s.t.Context())
	}
	if err != nil {
		s.t.Fatalf("%s: %v", op.name, err)
	}
	before := cloneRegistrySnapshotForMutation(s.model)
	entry := op.entry
	entry.Sequence = s.model.JournalSequence + 1
	if _, err := s.store.reducer.applyJournalEntry(context.Background(), &s.model, entry); err != nil {
		s.t.Fatal(err)
	}
	s.checkCaptures(index, op.name, before, s.mem.captures[start:])
	registryFSByDir.Store(s.dir, s.mem)
}

func (s *crashScenario) checkCaptures(index int, name string, before snapshot, captures []crashCapture) {
	s.t.Helper()
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		s.t.Fatal(err)
	}
	afterJSON, err := json.Marshal(s.model)
	if err != nil {
		s.t.Fatal(err)
	}
	for point, capture := range captures {
		for variant, image := range capture.images {
			recovered, err := s.recoverImage(image)
			if err != nil {
				s.t.Fatalf("op %d %s point %d %s variant %d: recovery: %v", index, name, point, capture.boundary, variant, err)
			}
			recoveredJSON, err := json.Marshal(recovered)
			if err != nil {
				s.t.Fatal(err)
			}
			if !bytes.Equal(beforeJSON, recoveredJSON) && !bytes.Equal(afterJSON, recoveredJSON) {
				s.t.Fatalf("op %d %s point %d %s variant %d: no valid prefix\nbefore: %s\nafter: %s\nrecovered: %s", index, name, point, capture.boundary, variant, beforeJSON, afterJSON, recoveredJSON)
			}
			s.checked++
		}
	}
}

func (s *crashScenario) recoverImage(image map[string][]byte) (snapshot, error) {
	registryFSByDir.Store(s.dir, crashFSFromImage(s.dir, image))
	recovered, err := s.store.loadSnapshot()
	if err != nil {
		return snapshot{}, err
	}
	entries, err := readJournal(s.path)
	if err != nil {
		return snapshot{}, err
	}
	if err := s.store.reducer.foldJournal(context.Background(), &recovered, entries); err != nil {
		return snapshot{}, err
	}
	if err := validateSnapshot(recovered); err != nil {
		return snapshot{}, err
	}
	return recovered, nil
}
