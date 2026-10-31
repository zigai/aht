//go:build linux || darwin

package observer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObserverLockRecoversExistingLockFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "observer.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := openObserverLock(path)
	if err != nil {
		t.Fatalf("existing lock file should be reusable: %v", err)
	}

	second, err := openObserverLock(path)
	if err == nil {
		_ = closeObserverLock(second)

		t.Fatal("second observer acquired an active lock")
	}

	if err := closeObserverLock(first); err != nil {
		t.Fatal(err)
	}

	third, err := openObserverLock(path)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}

	if err := closeObserverLock(third); err != nil {
		t.Fatal(err)
	}
}
