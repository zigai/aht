// Package drivers lists the supported terminal multiplexer drivers.
//
// [All] is ordered by precedence: when a process sits in panes of several
// nested multiplexers, the innermost one comes first and owns its location.
package drivers

import (
	"context"
	"fmt"

	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
	"github.com/zigai/aht/v2/pkg/zellij"
)

// All returns every driver in precedence order.
func All() []mux.Driver {
	return []mux.Driver{herdr.NewDriver(), zellij.NewDriver(), tmux.NewDriver()}
}

// Find returns the driver for kind.
func Find(kind registry.MultiplexerKind) (mux.Driver, bool) {
	for _, driver := range All() {
		if driver.Kind() == kind {
			return driver, true
		}
	}

	return nil, false
}

// Precedence ranks kind for ordering panes: a higher value comes first.
// Unknown kinds rank lowest.
func Precedence(kind registry.MultiplexerKind) int {
	all := All()
	for index, driver := range all {
		if driver.Kind() == kind {
			return len(all) - index
		}
	}

	return 0
}

// Current returns the caller's innermost multiplexer location, or an empty
// location when the caller is not inside a supported multiplexer.
func Current(ctx context.Context) (registry.Location, error) {
	for _, driver := range All() {
		location, err := driver.Current(ctx)
		if err != nil {
			return location, fmt.Errorf("current %s location: %w", driver.Kind(), err)
		}

		if !location.Empty() {
			return location, nil
		}
	}

	var empty registry.Location

	return empty, nil
}

// EnvKeys returns the environment variables any driver reads in LocationFromEnv.
func EnvKeys() []string {
	var keys []string
	for _, driver := range All() {
		keys = append(keys, driver.EnvKeys()...)
	}

	return keys
}

// LocationFromEnv returns the innermost pane that env places a process in.
func LocationFromEnv(env map[string]string) registry.Location {
	lookup := func(key string) string { return env[key] }
	for _, driver := range All() {
		if location := driver.LocationFromEnv(lookup); !location.Empty() {
			return location
		}
	}

	var empty registry.Location

	return empty
}
