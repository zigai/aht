package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zigai/aht/v2/internal/observer"
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
	ErrReconciliationIncomplete = errors.New(
		"tracker has not completed a successful reconciliation",
	)
)

// HealthStatus indicates the operational state of tracker reconciliation.
type HealthStatus string

// TrackerHealth is the tracker's health file plus its evaluated freshness.
type TrackerHealth struct {
	observer.Health

	Fresh   bool          `json:"fresh"`
	Age     time.Duration `json:"age"`
	Status  HealthStatus  `json:"status"`
	Message string        `json:"message,omitempty"`
}

// HealthPath returns the resolved filesystem path to the observer health sidecar.
func (m *Manager) HealthPath() string {
	storePath := m.config.StorePath
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}

	return observer.HealthPath(storePath)
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

	var health TrackerHealth

	raw, err := observer.ReadHealth(path)
	if errors.Is(err, os.ErrNotExist) {
		health.Status = HealthStatusMissing
		health.Message = "tracker health is missing; run aht manage tracker run " +
			"--once or aht manage tracker enable"

		return health, fmt.Errorf("%w: %s", ErrHealthMissing, path)
	}

	if err != nil {
		health.Status = HealthStatusCorrupt
		health.Message = err.Error()

		return health, fmt.Errorf("%w: %s: %w", ErrHealthCorrupt, path, err)
	}

	health.Health = raw

	return evaluateTrackerHealthFreshness(health, now, maxAge)
}

func evaluateTrackerHealthFreshness(
	health TrackerHealth,
	now time.Time,
	maxAge time.Duration,
) (TrackerHealth, error) {
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
		health.Message = fmt.Sprintf(
			"last reconciliation was %s ago (stale, limit %s)",
			age.Round(time.Second),
			effectiveMaxAge,
		)

		return health, fmt.Errorf("%w: %s", ErrHealthStale, health.Message)
	}

	health.Fresh = true
	health.Status = HealthStatusHealthy
	health.Message = "last successful reconciliation at " + health.LastSuccessAt.Format(
		time.RFC3339,
	)

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
