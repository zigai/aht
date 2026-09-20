package registry

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestNativePayloadRejectionPreservesStoreState(t *testing.T) {
	t.Parallel()
	file, memory, at := payloadStores(t)
	seed := payloadObservation("existing", at, `{"valid":true}`)
	for _, store := range []Store{file, memory} {
		if _, err := store.Observe(t.Context(), seed); err != nil {
			t.Fatal(err)
		}
	}
	before, err := memory.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	fileBefore, err := file.List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	storageRevision, changed := memory.storageRevision, memory.stateChanged
	batch := []Observation{
		payloadObservation("another", at.Add(time.Second), `{"valid":true}`),
		payloadObservation("existing", at.Add(time.Second), "{"),
	}
	_, fileErr := file.ObserveBatch(t.Context(), batch)
	_, memoryErr := memory.ObserveBatch(t.Context(), batch)
	assertPayloadErrorsEquivalent(t, fileErr, memoryErr)
	after, err := memory.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "rejected memory batch", before, after)
	if memory.storageRevision != storageRevision || memory.stateChanged != changed {
		t.Fatal("rejected batch advanced persistence or subscription state")
	}
	select {
	case <-changed:
		t.Fatal("rejected batch notified subscribers")
	default:
	}
	fileSessions, err := file.List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "rejected file batch", fileBefore, fileSessions)
	if err := memory.Flush(t.Context()); err != nil {
		t.Fatalf("retained state cannot be persisted: %v", err)
	}
}

func assertPayloadErrorsEquivalent(t *testing.T, fileErr, memoryErr error) {
	t.Helper()
	for _, err := range []error{fileErr, memoryErr} {
		if _, ok := errors.AsType[*json.MarshalerError](err); !ok {
			t.Fatalf("invalid retained payload error = %v, want JSON marshaling error", err)
		}
	}
	if fileErr.Error() != memoryErr.Error() {
		t.Fatalf("error mismatch: file %v, memory %v", fileErr, memoryErr)
	}
}

func TestNativePayloadValidationUsesFinalRetainedState(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"superseded", "ignored after end", "empty"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file, memory, at := payloadStores(t)
			first := payloadObservation("native", at, "{")
			last := payloadObservation("native", at.Add(time.Second), `{"valid":true}`)
			switch name {
			case "ignored after end":
				first.RawPayload = last.RawPayload
				first.Lifecycle = new(NativeLifecycleEnd)
				first.Activity = nil
				last.RawPayload = json.RawMessage("{")
			case "empty":
				first.RawPayload = nil
				last.RawPayload = json.RawMessage{}
			}
			for _, store := range []Store{file, memory} {
				if _, err := store.ObserveBatch(t.Context(), []Observation{first, last}); err != nil {
					t.Fatalf("discarded or empty payload rejected: %v", err)
				}
			}
			if err := memory.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			fileSessions, err := file.List(t.Context(), Filter{})
			if err != nil {
				t.Fatal(err)
			}
			// Compare persisted representations: the encoder may indent raw JSON.
			memorySessions, err := NewFileStore(memory.Path()).List(t.Context(), Filter{})
			if err != nil {
				t.Fatal(err)
			}
			assertEquivalent(t, "retained payload", fileSessions, memorySessions)
		})
	}
}

func payloadStores(t *testing.T) (*FileStore, *MemoryStore, time.Time) {
	t.Helper()
	file := NewFileStore(filepath.Join(t.TempDir(), "file.json"))
	memory, err := OpenMemoryStore(filepath.Join(t.TempDir(), "memory.json"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	file.setNowForTest(func() time.Time { return at.Add(time.Minute) })
	memory.setNowForTest(func() time.Time { return at.Add(time.Minute) })
	return file, memory, at
}

func payloadObservation(id string, at time.Time, raw string) Observation {
	return Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: id},
		Activity: new(ActivityRunning), NativeEvent: "test", ObservedAt: at,
		RawPayload: json.RawMessage(raw),
	}
}
