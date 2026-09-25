package registry

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func FuzzObservationCodec(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"harness":"sample","at":"2026-01-01T00:00:00Z","subject":{"session_id":"s"},"kind":"report","evidence":{"event":"start","payload":{"nested": [1, 2]}}}`),
		[]byte(`{"kind":"sighting","evidence":{"process":{"pid":12,"start_identity":"boot:12"},"present":true}}`),
		[]byte(`{"kind":"placement","evidence":{"location":{"kind":"tmux","pane_id":"%1"}}}`),
		[]byte(`{"kind":"listing","evidence":{"cwd":"/repo","current":true}}`),
		[]byte(`{"kind":"reading","evidence":{"activity":"waiting","reason":"prompt"}}`),
		[]byte(`{"kind":"unknown","evidence":{}}`),
		[]byte(`{"kind":"report","evidence":`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var value Observation
		if json.Unmarshal(data, &value) != nil {
			return
		}
		checkCodecRoundTrip(t, value, data)
	})
}

func FuzzSessionCodec(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"id":"s","harness":"sample","liveness":{"kind":"live","activity":"running"}}`),
		[]byte(`{"id":"gone","liveness":{"kind":"gone","at":"2026-01-01T00:00:00Z","reason":"ended"}}`),
		[]byte(`{"liveness":{"kind":"unknown","activity":"idle","decision":{"authority":"hook","reason":"event"}}}`),
		[]byte(`{"liveness":{"kind":"invalid","activity":"idle"}}`),
		[]byte(`{"liveness":{"kind":"gone","activity":"idle"}}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var value Session
		if json.Unmarshal(data, &value) != nil {
			return
		}
		checkCodecRoundTrip(t, value, data)
	})
}

func FuzzJournalEntryCodec(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"sequence":1,"received_at":"2026-01-01T00:00:00Z","observations":[{"kind":"report","evidence":{"event":"start"}}],"payloads":[{"nested":[1,2]}]}`),
		[]byte(`{"sequence":2,"observations":[{"kind":"sighting","evidence":{"present":true}}],"payloads":[null],"delete_after":123,"reset":true}`),
		[]byte(`{"observations":[{"kind":"report","evidence":{"payload":{"value":"legacy"}}}]}`),
		[]byte(`{"observations":[{"kind":"unknown","evidence":{}}]}`),
		[]byte(`{"observations":[{"kind":"report","evidence":{}}],"payloads":[`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var value journalEntry
		if json.Unmarshal(data, &value) != nil {
			return
		}
		checkCodecRoundTrip(t, value, data)
	})
}

func checkCodecRoundTrip[T any](t *testing.T, value T, seed []byte) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("seed=%q: encode decoded value: %v", seed, err)
	}
	var decoded T
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("seed=%q: decode encoded value: %v", seed, err)
	}
	// Raw JSON fields retain their value while encoding may normalize whitespace.
	// JSON omitempty collapses empty slices and maps to nil on decoding.
	equateEmpty := cmp.FilterPath(func(path cmp.Path) bool {
		return path.Last().Type() != reflect.TypeFor[json.RawMessage]()
	}, cmpopts.EquateEmpty())
	if diff := cmp.Diff(value, decoded, equateEmpty, cmp.Comparer(equalRawJSON), cmp.Comparer(func(a, b ProcessIdentity) bool { return a == b })); diff != "" {
		t.Fatalf("seed=%q: round-trip mismatch (-first +second):\n%s", seed, diff)
	}
	again, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("seed=%q: re-encode decoded value: %v", seed, err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatalf("seed=%q: encoding is unstable:\n%s\n%s", seed, encoded, again)
	}
}

func equalRawJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return bytes.Equal(a, b)
	}
	return cmp.Equal(left, right)
}
