package core

import (
	"errors"
	"testing"
	"time"
)

func TestGoneSessionCannotHaveActivity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	err := Sessions([]Session{{ID: "gone", Harness: "fixture", CreatedAt: at, UpdatedAt: at, HasLiveness: true, Presence: "gone", Activity: "running", ValidState: true}})
	if !errors.Is(err, errGoneActivity) {
		t.Fatalf("gone activity error = %v", err)
	}
}
