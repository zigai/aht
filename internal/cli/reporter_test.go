package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestReportDecodesTypedReporter(t *testing.T) {
	t.Parallel()
	for name, flags := range map[string][]string{
		"typed": {"--reporter", "pi-extension", "--reporter-version", "9", "--multi-session"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := append(make([]string, 0, 16+len(flags)), "--store", filepath.Join(t.TempDir(), "sessions.json"), "--json", "report", "pi", "--session-id", "reporter", "--event", "heartbeat", "--activity", "idle", "--no-tmux", "--sequence", "42", "--attribute", "native_key=kept")
			args = append(args, flags...)
			var stdout, stderr bytes.Buffer
			if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			var session registry.Session
			if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			if session.Observations.Native == nil {
				t.Fatal("missing report")
			}
			want := registry.Reporter{Integration: "pi-extension", Version: 9, MultiSession: true, Sequence: new(uint64(42))}
			if !reflect.DeepEqual(session.Observations.Native.Reporter, want) {
				t.Fatalf("reporter=%#v", session.Observations.Native.Reporter)
			}
			if !reflect.DeepEqual(session.Observations.Native.Attributes, map[string]string{"native_key": "kept"}) {
				t.Fatalf("untyped reporter metadata leaked: %#v", session.Observations.Native.Attributes)
			}
		})
	}
}
