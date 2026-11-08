package observer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const maxHealthBytes = 1 << 20

var errHealthTooLarge = errors.New("observer health exceeds 1 MiB")

// Health is the observer's health file. The tracker writes it next to the
// registry store; [ReadHealth] reads it back.
type Health struct {
	PID                          int           `json:"pid"`
	Interval                     time.Duration `json:"interval"`
	GracePeriod                  time.Duration `json:"grace_period"`
	StartedAt                    time.Time     `json:"started_at"`
	LastAttemptAt                time.Time     `json:"last_attempt_at"`
	LastSuccessAt                time.Time     `json:"last_success_at"`
	LastEnumerationErrorCategory string        `json:"last_enumeration_error_category,omitempty"`
	LastEnumerationError         string        `json:"last_enumeration_error,omitempty"`
	Cycles                       int64         `json:"cycles"`
	Observations                 int64         `json:"observations"`
	Sessions                     int64         `json:"sessions"`
	Degraded                     bool          `json:"degraded"`
}

// HealthPath returns the health file path for the registry store at storePath.
func HealthPath(storePath string) string {
	return storePath + ".observer-health.json"
}

// ReadHealth reads the health file at path. A missing file returns an error
// matching [os.ErrNotExist].
func ReadHealth(path string) (Health, error) {
	var health Health

	file, err := os.Open(path)
	if err != nil {
		return health, fmt.Errorf("opening observer health: %w", err)
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxHealthBytes+1))
	if err != nil {
		return health, fmt.Errorf("reading observer health: %w", err)
	}

	if len(data) > maxHealthBytes {
		return health, errHealthTooLarge
	}

	if err := json.Unmarshal(data, &health); err != nil {
		return health, fmt.Errorf("decoding observer health: %w", err)
	}

	return health, nil
}

func lockPath(storePath string) string {
	return storePath + ".observer.lock"
}
