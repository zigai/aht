package claude

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
			title, err := scanSessionTitle(ctx, bufio.NewReader(reader), "native")
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
