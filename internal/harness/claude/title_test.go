package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/registry"
)

func writeTitleTranscript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSessionTitlesUsesNewestNativeTitleWithManualPrecedence(t *testing.T) {
	isolateTitleCache(t)
	for _, tt := range []struct{ name, body, want string }{
		{"generated", `{"type":"ai-title","sessionId":"native","aiTitle":"Old"}
{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}
`, "Generated"},
		{"manual before generated", `{"type":"custom-title","sessionId":"native","customTitle":"Old"}
{"type":"custom-title","sessionId":"native","customTitle":"Manual"}
{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}
`, "Manual"},
		{"manual after generated", `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}
{"type":"custom-title","sessionId":"native","customTitle":"Manual"}
`, "Manual"},
		{"empty manual falls back", `{"type":"custom-title","sessionId":"native","customTitle":"Old"}
{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}
{"type":"custom-title","sessionId":"native","customTitle":""}
`, "Generated"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTitleTranscript(t, tt.body)
			titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "native", SessionPath: path}})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff([]string{tt.want}, titles); diff != "" {
				t.Fatalf("titles mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSessionTitlesSkipsForeignMalformedAndOversizedRecords(t *testing.T) {
	isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}
{"type":"custom-title","sessionId":"foreign","customTitle":"Foreign"}
{"type":"custom-title","customTitle":"Missing identity"}
{"type":"custom-title","sessionId":"native","customTitle":null}
{"type":"custom-title","sessionId":"native","customTitle":42}
{"type":"custom-title","sessionId":"native"}
{"type":"ai-title","sessionId":"native","aiTitle":{}}
malformed
`+`{"type":"message","message":{"content":"`+strings.Repeat("x", 1<<20)+`"}}`+"\n"+
		`{"type":"ai-title","sessionId":"native","aiTitle":"Current"}`+"\n"+
		`{"type":"custom-title","sessionId":"native","customTitle":"incomplete`)
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "native", SessionPath: path},
		{SessionID: "unmatched", SessionPath: path},
		{SessionID: "missing", SessionPath: filepath.Join(t.TempDir(), "absent.jsonl")},
		{SessionPath: path},
		{SessionID: "native"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Current", "", "", "", ""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionTitlesPreservesSuccessesAndJoinsFileFailures(t *testing.T) {
	isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"custom-title","sessionId":"native","customTitle":"Manual"}`)
	root := t.TempDir()
	link := filepath.Join(root, "link.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "native", SessionPath: link},
		{SessionID: "native", SessionPath: path},
		{SessionID: "native", SessionPath: root},
	})
	if err == nil {
		t.Fatal("nonregular sources did not report errors")
	}
	if joined, ok := err.(interface{ Unwrap() []error }); !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("independent failures were not joined: %v", err)
	}
	if diff := cmp.Diff([]string{"", "Manual", ""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionTitlesHonorsCancellation(t *testing.T) {
	isolateTitleCache(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	titles, err := New().SessionTitles(ctx, []registry.ObservationIdentity{{SessionID: "native", SessionPath: "unused"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup error = %v", err)
	}
	if diff := cmp.Diff([]string{""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestScanSessionTitleDropsCurrentTitleOnCancellationAndReadFailure(t *testing.T) {
	t.Parallel()
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "read failure", true: "cancellation"}[canceled], func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := io.ErrUnexpectedEOF
			if canceled {
				failure = context.Canceled
			}
			reader := &failingTitleReader{body: strings.NewReader(`{"type":"custom-title","sessionId":"native","customTitle":"Manual"}` + "\n"), err: failure}
			if canceled {
				reader.cancel = cancel
			}
			title, err := scanSessionTitle(ctx, reader, "native", &titleScanState{})
			if title != "" || !errors.Is(err, failure) {
				t.Fatalf("title, error = %q, %v", title, err)
			}
		})
	}
}

type failingTitleReader struct {
	body   *strings.Reader
	err    error
	cancel context.CancelFunc
}

func (r *failingTitleReader) Read(p []byte) (int, error) {
	if r.body.Len() > 0 {
		n, err := r.body.Read(p)
		if err != nil {
			return n, fmt.Errorf("read title fixture: %w", err)
		}
		return n, nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	return 0, r.err
}

func requireCachedTitle(t *testing.T, path, want string) {
	t.Helper()
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "native", SessionPath: path}})
	if err != nil || len(titles) != 1 || titles[0] != want {
		t.Fatalf("titles, error = %q, %v; want %q", titles, err, want)
	}
}

func appendTitleTranscript(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(body)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
}

func titleCacheSnapshot(t *testing.T, cacheRoot string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(filepath.Join(cacheRoot, "aht"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil || len(paths) != 1 {
		t.Fatalf("cache snapshots = %v, error = %v; want one persisted snapshot", paths, err)
	}
	return paths[0]
}

func TestTitleCachePersistsAcrossAdaptersAndAppends(t *testing.T) {
	cacheRoot := isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"First"}`+"\n")
	requireCachedTitle(t, path, "First")
	snapshot := titleCacheSnapshot(t, cacheRoot)
	info, err := os.Stat(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache permissions = %o, want 600", info.Mode().Perm())
	}
	for dir := filepath.Dir(snapshot); dir != cacheRoot; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("cache directory permissions: %v, %v", info, err)
		}
	}
	requireCachedTitle(t, path, "First")
	appendTitleTranscript(t, path, `{"type":"ai-title","sessionId":"native","aiTitle":"Second"}`+"\n")
	requireCachedTitle(t, path, "Second")
	appendTitleTranscript(t, path, `{"type":"custom-title","sessionId":"native","customTitle":"Manual"}`+"\n"+
		`{"type":"ai-title","sessionId":"native","aiTitle":"Latest"}`+"\n")
	requireCachedTitle(t, path, "Manual")
	appendTitleTranscript(t, path, `{"type":"custom-title","sessionId":"native","customTitle":""}`+"\n")
	requireCachedTitle(t, path, "Latest")
}

func TestTitleCacheReplaysUnterminatedTails(t *testing.T) {
	isolateTitleCache(t)
	for _, tt := range []struct{ name, tail, continuation, first, want string }{
		{"valid becomes malformed", `{"type":"custom-title","sessionId":"native","customTitle":"Temporary"}`, "garbage\n", "Temporary", "Generated"},
		{"malformed completed", `{"type":"custom-title","sessionId":"native","customTitle":"Manual`, "\"}\n", "Generated", "Manual"},
		{"oversized completed", strings.Repeat("x", (1<<20)+17), "\n" + `{"type":"custom-title","sessionId":"native","customTitle":"Manual"}` + "\n", "Generated", "Manual"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}`+"\n"+tt.tail)
			requireCachedTitle(t, path, tt.first)
			requireCachedTitle(t, path, tt.first)
			appendTitleTranscript(t, path, tt.continuation)
			requireCachedTitle(t, path, tt.want)
		})
	}
}

func TestTitleCacheInvalidatesReplacementTruncationAndRewrite(t *testing.T) {
	isolateTitleCache(t)
	for _, tt := range []struct {
		name   string
		change func(*testing.T, string, string)
	}{
		{"replace", func(t *testing.T, path, body string) {
			t.Helper()
			replacement := filepath.Join(filepath.Dir(path), "replacement")
			if err := os.WriteFile(replacement, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"truncate", func(t *testing.T, path, body string) {
			t.Helper()
			if err := os.Truncate(path, 0); err != nil {
				t.Fatal(err)
			}
			requireCachedTitle(t, path, "")
			appendTitleTranscript(t, path, body)
		}},
		{"same size restored mtime", func(t *testing.T, path, body string) {
			t.Helper()
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			old := `{"type":"ai-title","sessionId":"native","aiTitle":"Old"}` + "\n"
			path := writeTitleTranscript(t, old)
			requireCachedTitle(t, path, "Old")
			tt.change(t, path, strings.Replace(old, "Old", "New", 1))
			requireCachedTitle(t, path, "New")
		})
	}
}

func titleCacheObject(t *testing.T, record map[string]any, key string) map[string]any {
	t.Helper()
	object, ok := record[key].(map[string]any)
	if !ok {
		t.Fatalf("cache %s type = %T; want object", key, record[key])
	}
	return object
}

func TestTitleCacheRejectsCorruptOrForeignSnapshots(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{"previous version", func(_ *testing.T, record map[string]any) { record["version"] = 1 }},
		{"negative offset", func(t *testing.T, record map[string]any) {
			t.Helper()
			titleCacheObject(t, record, "state")["offset"] = -1
		}},
		{"past eof offset", func(t *testing.T, record map[string]any) {
			t.Helper()
			titleCacheObject(t, record, "state")["offset"] = 1 << 30
		}},
		{"missing state", func(_ *testing.T, record map[string]any) { delete(record, "state") }},
		{"missing title", func(_ *testing.T, record map[string]any) { delete(record, "title") }},
		{"invalid skipping", func(t *testing.T, record map[string]any) {
			t.Helper()
			state := titleCacheObject(t, record, "state")
			state["offset"] = 1
			state["skipping"] = true
		}},
		{"missing identity", func(t *testing.T, record map[string]any) {
			t.Helper()
			delete(titleCacheObject(t, record, "stamp"), "inode")
		}},
		{"wrong identity", func(t *testing.T, record map[string]any) {
			t.Helper()
			titleCacheObject(t, record, "stamp")["inode"] = 1 << 30
		}},
		{"foreign path", func(t *testing.T, record map[string]any) {
			t.Helper()
			record["path"] = filepath.Join(t.TempDir(), "foreign")
		}},
		{"foreign session", func(_ *testing.T, record map[string]any) { record["session_id"] = "foreign" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cacheRoot := isolateTitleCache(t)
			path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}`+"\n")
			requireCachedTitle(t, path, "Generated")
			snapshot := titleCacheSnapshot(t, cacheRoot)
			body, err := os.ReadFile(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.UseNumber()
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			record["title"] = "Stale"
			tt.mutate(t, record)
			body, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(snapshot, body, 0o600); err != nil {
				t.Fatal(err)
			}
			requireCachedTitle(t, path, "Generated")
		})
	}
	t.Run("corrupt", func(t *testing.T) {
		cacheRoot := isolateTitleCache(t)
		path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}`+"\n")
		requireCachedTitle(t, path, "Generated")
		if err := os.WriteFile(titleCacheSnapshot(t, cacheRoot), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		requireCachedTitle(t, path, "Generated")
	})
}

func obstructTitleCacheSnapshot(t *testing.T, cacheRoot, path string) {
	t.Helper()
	requireCachedTitle(t, path, "Manual")
	snapshot := titleCacheSnapshot(t, cacheRoot)
	if err := os.Remove(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestTitleCacheErrorsPreserveScannedTitle(t *testing.T) {
	for _, tt := range []struct {
		name, want string
		obstruct   func(*testing.T, string, string)
	}{
		{"directory", "Manual", func(t *testing.T, cacheRoot, _ string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(cacheRoot, "aht"), []byte("blocked"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"read", "Manual", obstructTitleCacheSnapshot},
		{"publish", "Newest", func(t *testing.T, cacheRoot, path string) {
			t.Helper()
			obstructTitleCacheSnapshot(t, cacheRoot, path)
			appendTitleTranscript(t, path, `{"type":"custom-title","sessionId":"native","customTitle":"Newest"}`+"\n")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cacheRoot := isolateTitleCache(t)
			path := writeTitleTranscript(t, `{"type":"custom-title","sessionId":"native","customTitle":"Manual"}`+"\n")
			tt.obstruct(t, cacheRoot, path)
			titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "native", SessionPath: path}})
			if err == nil || len(titles) != 1 || titles[0] != tt.want {
				t.Fatalf("titles, error = %q, %v; want %q and cache error", titles, err, tt.want)
			}
		})
	}
}

func TestTitleCacheRechecksSourceOnHit(t *testing.T) {
	for _, tt := range []struct {
		name    string
		replace func(*testing.T, string)
		wantErr bool
	}{
		{"symlink", func(t *testing.T, path string) {
			t.Helper()
			target := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Foreign"}`+"\n")
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"directory", func(t *testing.T, path string) {
			t.Helper()
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"missing", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateTitleCache(t)
			path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}`+"\n")
			requireCachedTitle(t, path, "Generated")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if tt.replace != nil {
				tt.replace(t, path)
			}
			title, err := readSessionTitle(t.Context(), "native", path)
			if title != "" || (err != nil) != tt.wantErr {
				t.Fatalf("title, error = %q, %v; want empty title, error presence %t", title, err, tt.wantErr)
			}
		})
	}
}

func TestTitleCacheRechecksCancellationOnHit(t *testing.T) {
	isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}`+"\n")
	requireCachedTitle(t, path, "Generated")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	title, err := readSessionTitle(ctx, "native", path)
	if title != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("title, error = %q, %v; want empty title and cancellation", title, err)
	}
}

func TestTitleScannerHandlesExact64KiBBoundaries(t *testing.T) {
	for _, size := range []int{(64 << 10) - 1, 64 << 10, (64 << 10) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			record := `{"type":"message","padding":"`
			record += strings.Repeat("x", size-len(record)-3) + "\"}\n"
			body := record + `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}` + "\n"
			title, err := scanSessionTitle(t.Context(), strings.NewReader(body), "native", &titleScanState{})
			if title != "Generated" || err != nil {
				t.Fatalf("title, error = %q, %v", title, err)
			}
		})
	}
}

func TestTitleCacheCatchesUpAfterOlderConcurrentPublication(t *testing.T) {
	cacheRoot := isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Old"}`+"\n")
	requireCachedTitle(t, path, "Old")
	snapshot := titleCacheSnapshot(t, cacheRoot)
	old, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	appendTitleTranscript(t, path, `{"type":"ai-title","sessionId":"native","aiTitle":"Current"}`+"\n")
	requireCachedTitle(t, path, "Current")
	root, err := os.OpenRoot(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cacheRoot, snapshot)
	if err != nil {
		t.Fatal(errors.Join(err, root.Close()))
	}
	if err := errors.Join(root.WriteFile(relative, old, 0o600), root.Close()); err != nil {
		t.Fatal(err)
	}
	requireCachedTitle(t, path, "Current")
	requireCachedTitle(t, path, "Current")
}

func TestTitleCacheSeparatesNativeSessionsAndSourcePaths(t *testing.T) {
	isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Native"}`+"\n"+
		`{"type":"custom-title","sessionId":"foreign","customTitle":"Foreign"}`+"\n")
	other := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Other"}`+"\n")
	for range 2 {
		titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
			{SessionID: "native", SessionPath: path},
			{SessionID: "foreign", SessionPath: path},
			{SessionID: "native", SessionPath: other},
		})
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]string{"Native", "Foreign", "Other"}, titles); diff != "" {
			t.Fatalf("titles mismatch (-want +got):\n%s", diff)
		}
	}
}

func TestTitleScannerRecordSizeLimit(t *testing.T) {
	for _, size := range []int{(64 << 10) - 1, 64 << 10, (64 << 10) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			prefix := `{"type":"custom-title","sessionId":"native","customTitle":"`
			titleValue := strings.Repeat("x", size-len(prefix)-3)
			record := prefix + titleValue + "\"}\n"
			want := titleValue
			if size > 64<<10 {
				want = "Generated"
			}
			body := `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}` + "\n" + record
			title, err := scanSessionTitle(t.Context(), strings.NewReader(body), "native", &titleScanState{})
			if title != want || err != nil {
				t.Fatalf("title length, error = %d, %v; want length %d", len(title), err, len(want))
			}
		})
	}
}

func TestTitleScannerValidatesTopLevelJSONFields(t *testing.T) {
	for _, tt := range []struct{ name, record, want string }{
		{"fields reordered", `{"aiTitle":"Title","sessionId":"native","type":"ai-title"}`, "Title"},
		{"escaped fields and values", `{"ty\u0070e":"custom-title","session\u0049d":"nat\u0069ve","customTitle":"A \"quote\" \\ slash \u00e9"}`, "A \"quote\" \\ slash é"},
		{"nested fake title", `{"type":"message","sessionId":"native","nested":{"type":"custom-title","customTitle":"Fake"}}`, "Generated"},
		{"nested session", `{"type":"custom-title","nested":{"sessionId":"native"},"customTitle":"Fake"}`, "Generated"},
		{"invalid suffix", `{"type":"custom-title","sessionId":"native","customTitle":"Fake"} garbage`, "Generated"},
		{"invalid unrelated value", `{"type":"custom-title","sessionId":"native","customTitle":"Fake","other":[1,]}`, "Generated"},
		{"duplicate fields last wins", `{"type":"ai-title","type":"custom-title","sessionId":"foreign","sessionId":"native","customTitle":"First","customTitle":"Last"}`, "Last"},
		{"escaped newline", `{"type":"custom-title","sessionId":"native","customTitle":"First\nSecond"}`, "First\nSecond"},
		{"nonobject root", `[{"type":"custom-title","sessionId":"native","customTitle":"Fake"}]`, "Generated"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}` + "\n" + tt.record + "\n"
			title, err := scanSessionTitle(t.Context(), strings.NewReader(body), "native", &titleScanState{})
			if title != tt.want || err != nil {
				t.Fatalf("title, error = %q, %v; want %q", title, err, tt.want)
			}
		})
	}
}

func isolateTitleCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("HOME", root)
	cacheRoot := root
	if runtime.GOOS == "darwin" {
		cacheRoot = filepath.Join(root, "Library", "Caches")
	}
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return cacheRoot
}

func TestTitleCacheRejectsUnreadableSourceAfterPrime(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses source file read permissions")
	}
	isolateTitleCache(t)
	path := writeTitleTranscript(t, `{"type":"ai-title","sessionId":"native","aiTitle":"Generated"}`+"\n")
	requireCachedTitle(t, path, "Generated")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	title, err := readSessionTitle(t.Context(), "native", path)
	if title != "" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("title, error = %q, %v; want empty title and permission error", title, err)
	}
}
