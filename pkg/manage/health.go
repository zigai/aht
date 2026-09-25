package manage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	// HealthStatusHealthy indicates that the tracker is reconciling normally.
	HealthStatusHealthy HealthStatus = "healthy"

	// HealthStatusDegraded indicates that the tracker reported reconciliation failures.
	HealthStatusDegraded HealthStatus = "degraded"

	// HealthStatusStale indicates that the tracker health sidecar is older than the expected interval.
	HealthStatusStale HealthStatus = "stale"

	// HealthStatusMissing indicates that no tracker health sidecar exists on disk.
	HealthStatusMissing HealthStatus = "missing"

	// HealthStatusCorrupt indicates that the tracker health sidecar contains invalid data.
	HealthStatusCorrupt HealthStatus = "corrupt"
)

var (
	// ErrHealthMissing indicates that the tracker health sidecar does not exist.
	ErrHealthMissing = errors.New("tracker health is missing")

	// ErrHealthStale indicates that the tracker has not completed reconciliation within the expected window.
	ErrHealthStale = errors.New("tracker health is stale")

	// ErrHealthDegraded indicates that the tracker reported degraded reconciliation or enumeration errors.
	ErrHealthDegraded = errors.New("tracker health reports degraded reconciliation")

	// ErrHealthCorrupt indicates that the tracker health sidecar is malformed or unreadable.
	ErrHealthCorrupt = errors.New("invalid tracker health sidecar")

	// ErrReconciliationIncomplete indicates that the tracker has not yet completed a successful cycle.
	ErrReconciliationIncomplete = errors.New("tracker has not completed a successful reconciliation")
)

// HealthStatus indicates the operational state of tracker reconciliation.
type HealthStatus string

// TrackerHealth represents background tracker reconciliation health, freshness, and counters.
type TrackerHealth struct {
	PID                          int           `json:"pid"`
	StartIdentity                string        `json:"start_identity,omitempty"`
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
	Fresh                        bool          `json:"fresh"`
	Age                          time.Duration `json:"age"`
	Status                       HealthStatus  `json:"status"`
	Message                      string        `json:"message,omitempty"`
}

type observerHealthRaw struct {
	PID                          int           `json:"pid"`
	StartIdentity                string        `json:"start_identity,omitempty"`
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

// HealthPath returns the resolved filesystem path to the observer health sidecar.
func (m *Manager) HealthPath() string {
	storePath := m.config.StorePath
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}
	return storePath + ".observer-health.json"
}

// TrackerHealth returns the reconciliation health of the managed background tracker.
func (m *Manager) TrackerHealth(ctx context.Context) (TrackerHealth, error) {
	if err := ctx.Err(); err != nil {
		var empty TrackerHealth
		return empty, fmt.Errorf("tracker health: %w", err)
	}
	return ReadTrackerHealth(m.HealthPath(), time.Now().UTC(), 0)
}

// ReadTrackerHealth reads and evaluates the tracker health sidecar at path.
// When now is zero, [time.Now].UTC() is used. When maxAge is zero or negative,
// a default limit of 3*Interval (minimum 2 minutes, default 2 minutes) is applied.
func ReadTrackerHealth(path string, now time.Time, maxAge time.Duration) (TrackerHealth, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	data, err := readHealthFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TrackerHealth{
				PID:                          0,
				StartIdentity:                "",
				Interval:                     0,
				GracePeriod:                  0,
				StartedAt:                    time.Time{},
				LastAttemptAt:                time.Time{},
				LastSuccessAt:                time.Time{},
				LastEnumerationErrorCategory: "",
				LastEnumerationError:         "",
				Cycles:                       0,
				Observations:                 0,
				Sessions:                     0,
				Degraded:                     false,
				Fresh:                        false,
				Age:                          0,
				Status:                       HealthStatusMissing,
				Message:                      "tracker health is missing; run aht manage tracker run --once or aht manage tracker enable",
			}, fmt.Errorf("%w: %s", ErrHealthMissing, path)
		}
		return TrackerHealth{
			PID:                          0,
			StartIdentity:                "",
			Interval:                     0,
			GracePeriod:                  0,
			StartedAt:                    time.Time{},
			LastAttemptAt:                time.Time{},
			LastSuccessAt:                time.Time{},
			LastEnumerationErrorCategory: "",
			LastEnumerationError:         "",
			Cycles:                       0,
			Observations:                 0,
			Sessions:                     0,
			Degraded:                     false,
			Fresh:                        false,
			Age:                          0,
			Status:                       HealthStatusCorrupt,
			Message:                      err.Error(),
		}, fmt.Errorf("%w: reading tracker health sidecar %s: %w", ErrHealthCorrupt, path, err)
	}

	var raw observerHealthRaw
	if unmarshalErr := json.Unmarshal(data, &raw); unmarshalErr != nil {
		return TrackerHealth{
			PID:                          0,
			StartIdentity:                "",
			Interval:                     0,
			GracePeriod:                  0,
			StartedAt:                    time.Time{},
			LastAttemptAt:                time.Time{},
			LastSuccessAt:                time.Time{},
			LastEnumerationErrorCategory: "",
			LastEnumerationError:         "",
			Cycles:                       0,
			Observations:                 0,
			Sessions:                     0,
			Degraded:                     false,
			Fresh:                        false,
			Age:                          0,
			Status:                       HealthStatusCorrupt,
			Message:                      "invalid observer health sidecar: " + unmarshalErr.Error(),
		}, fmt.Errorf("%w: %s: %w", ErrHealthCorrupt, path, unmarshalErr)
	}

	health := TrackerHealth{
		PID:                          raw.PID,
		StartIdentity:                raw.StartIdentity,
		Interval:                     raw.Interval,
		GracePeriod:                  raw.GracePeriod,
		StartedAt:                    raw.StartedAt,
		LastAttemptAt:                raw.LastAttemptAt,
		LastSuccessAt:                raw.LastSuccessAt,
		LastEnumerationErrorCategory: raw.LastEnumerationErrorCategory,
		LastEnumerationError:         raw.LastEnumerationError,
		Cycles:                       raw.Cycles,
		Observations:                 raw.Observations,
		Sessions:                     raw.Sessions,
		Degraded:                     raw.Degraded,
		Fresh:                        false,
		Age:                          0,
		Status:                       HealthStatusHealthy,
		Message:                      "",
	}

	return evaluateTrackerHealthFreshness(health, now, maxAge)
}

func evaluateTrackerHealthFreshness(health TrackerHealth, now time.Time, maxAge time.Duration) (TrackerHealth, error) {
	if health.Degraded || health.LastEnumerationError != "" {
		health.Status = HealthStatusDegraded
		health.Fresh = false
		health.Message = health.LastEnumerationError
		if health.Message == "" {
			health.Message = "tracker reports degraded reconciliation"
		}
		return health, fmt.Errorf("%w: %s", ErrHealthDegraded, health.Message)
	}

	if health.LastSuccessAt.IsZero() {
		health.Status = HealthStatusDegraded
		health.Fresh = false
		health.Message = "observer has not completed a successful reconciliation"
		return health, fmt.Errorf("%w", ErrReconciliationIncomplete)
	}

	age := max(0, now.Sub(health.LastSuccessAt))
	health.Age = age

	effectiveMaxAge := resolveEffectiveMaxAge(health.Interval, maxAge)
	if age > effectiveMaxAge {
		health.Fresh = false
		health.Status = HealthStatusStale
		health.Message = fmt.Sprintf("last reconciliation was %s ago (stale, limit %s)", age.Round(time.Second), effectiveMaxAge)
		return health, fmt.Errorf("%w: %s", ErrHealthStale, health.Message)
	}

	health.Fresh = true
	health.Status = HealthStatusHealthy
	health.Message = "last successful reconciliation at " + health.LastSuccessAt.Format(time.RFC3339)
	return health, nil
}

func resolveEffectiveMaxAge(interval time.Duration, maxAge time.Duration) time.Duration {
	if maxAge > 0 {
		return maxAge
	}
	const (
		minEffectiveAge     = 2 * time.Minute
		defaultEffectiveAge = 2 * time.Minute
		intervalMultiplier  = 3
	)
	if interval > 0 {
		effective := intervalMultiplier * interval
		if effective < minEffectiveAge {
			return minEffectiveAge
		}
		return effective
	}
	return defaultEffectiveAge
}

func readHealthFile(path string) ([]byte, error) {
	const maxHealthBytes = 1 << 20
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open health sidecar: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxHealthBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read health sidecar: %w", err)
	}
	if len(data) > maxHealthBytes {
		return nil, fmt.Errorf("%w: sidecar exceeds 1 MiB", ErrHealthCorrupt)
	}
	return data, nil
}
