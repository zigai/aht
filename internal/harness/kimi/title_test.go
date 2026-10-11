package kimi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func writeKimiState(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name, "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(path)
}

func TestKimiSessionTitlesReadLocalState(t *testing.T) {
	t.Parallel()
	sessionDir := writeKimiState(t, t.TempDir(), "session-example", `{"id":"session-example","title":"  Example title  ","cwd":"/work/example"}`)
	identities := []registry.ObservationIdentity{
		{SessionID: "session-example", SessionPath: sessionDir},
		{SessionID: "session-example", SessionPath: filepath.Join(sessionDir, "agents", "main", "wire.jsonl")},
		{SessionID: "session-example", SessionPath: filepath.Join(sessionDir, "state.json")},
		{SessionID: "session-example", SessionPath: filepath.Join(sessionDir, "agents", "agent-0", "wire.jsonl")},
		{SessionID: "other", SessionPath: sessionDir},
	}
	titles, err := (kimiCodeHarness{}).SessionTitles(t.Context(), identities)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Example title", "Example title", "Example title", "", ""}, titles); diff != "" {
		t.Fatalf("titles (-want +got):\n%s", diff)
	}
}

func TestKimiSessionTitleFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body string
		want       error
	}{
		{"invalid", `{"id":`, transcript.ErrInvalidRecord},
		{"wrong identity", `{"id":"another","title":"Wrong title"}`, transcript.ErrUnknownFormat},
		{"too large", strings.Repeat("x", transcript.MaxRecordBytes+1), transcript.ErrRecordSize},
		{"no title", `{"id":"session-example","lastPrompt":"Not a title"}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sessionDir := writeKimiState(t, t.TempDir(), "session-example", test.body)
			titles, err := (kimiCodeHarness{}).SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "session-example", SessionPath: sessionDir}})
			if !errors.Is(err, test.want) || len(titles) != 1 || titles[0] != "" {
				t.Fatalf("titles=%#v error=%v want=%v", titles, err, test.want)
			}
		})
	}
}

func TestKimiSessionTitlesMissingAndCancelled(t *testing.T) {
	t.Parallel()
	identity := registry.ObservationIdentity{SessionID: "session-example", SessionPath: filepath.Join(t.TempDir(), "session-example")}
	titles, err := (kimiCodeHarness{}).SessionTitles(t.Context(), []registry.ObservationIdentity{identity})
	if err != nil || len(titles) != 1 || titles[0] != "" {
		t.Fatalf("missing state=%#v, %v", titles, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = (kimiCodeHarness{}).SessionTitles(ctx, []registry.ObservationIdentity{identity})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup=%v", err)
	}
}
