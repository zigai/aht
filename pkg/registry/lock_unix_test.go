//go:build linux || darwin

package registry

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreMutationsCancelWhileLockHeld(t *testing.T) {
	for _, operation := range []string{"observe", "gc", "reset"} {
		t.Run(operation, func(t *testing.T) {
			testFileStoreMutationCancelsWhileLockHeld(t, operation)
		})
	}
}

func testFileStoreMutationCancelsWhileLockHeld(t *testing.T, operation string) {
	t.Helper()
	store := NewJournal(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	lock, err := openStoreLock(t.Context(), store.Path()+".lock")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	contended := make(chan struct{})
	store.setOnLockContentionForTest(func() {
		close(contended)
	})

	done := make(chan error, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Errorf("mutation goroutine failed to join")
		}
		if closeErr := lock.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	go func() {
		defer close(joined)
		done <- runBlockedMutation(ctx, store, operation)
	}()

	select {
	case <-contended:
	case <-time.After(3 * time.Second):
		t.Fatal("mutation did not reach lock contention")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("mutation error = %v, want cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mutation did not stop while the store lock remained held")
	}

	releaseTestStoreLock(t, lock)
	lock = nil
	if sessions, listErr := store.List(t.Context(), Filter{}); listErr != nil || len(sessions) != 0 {
		t.Fatalf("store committed sessions despite canceled mutation: %v, %v", sessions, listErr)
	}
}

func runBlockedMutation(ctx context.Context, store *Journal, operation string) error {
	switch operation {
	case "observe":
		act := ActivityRunning
		_, err := store.Observe(ctx, Observation{Harness: HarnessCodex, At: time.Now().UTC(), Subject: ObservationIdentity{SessionID: "blocked"}, Evidence: &Report{Event: "agent_start", Activity: &act}})
		return err
	case "gc":
		_, err := store.GC(ctx, 0)
		return err
	case "reset":
		_, err := store.Reset(ctx)
		return err
	default:
		return nil
	}
}

func TestStoreLockDeadlineWhileHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	lock, err := openStoreLock(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	second, err := openStoreLock(ctx, path)
	if second != nil {
		if closeErr := second.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock error = %v, want deadline exceeded", err)
	}
}

func releaseTestStoreLock(t *testing.T, lock *storeLock) {
	t.Helper()
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
