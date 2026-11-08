package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zigai/aht/v2/internal/config"

	"github.com/zigai/aht/v2/pkg/registry"
)

type sessionCompareFunc func(registry.Session, registry.Session) int

func sortListSessions(ss []registry.Session, opts listOptions) error {
	key, e := listSortKey(opts.sortBy)
	if e != nil {
		return e
	}

	compare, e := listSortLess(key)
	if e != nil {
		return e
	}

	sort.SliceStable(ss, func(i, j int) bool {
		v := compare(ss[i], ss[j])
		if opts.desc {
			return v > 0
		}

		return v < 0
	})

	return nil
}

func listSortLess(k string) (sessionCompareFunc, error) {
	if c, ok := map[string]sessionCompareFunc{
		"multiplexer": compareSessionMultiplexer,
		"tmux":        compareSessionTmux,
		"updated":     compareSessionUpdated,
		"presence-changed": func(a, b registry.Session) int {
			return a.PresenceChangedAt.Compare(
				b.PresenceChangedAt,
			)
		},
		"activity-changed": func(a, b registry.Session) int {
			return a.ActivityChangedAt.Compare(
				b.ActivityChangedAt,
			)
		},
		"created": compareSessionCreated,
		"harness": func(a, b registry.Session) int {
			return strings.Compare(
				string(
					a.Harness,
				),
				string(
					b.Harness,
				),
			)
		},
		"presence": func(a, b registry.Session) int {
			return strings.Compare(
				string(
					a.Presence(),
				),
				string(
					b.Presence(),
				),
			)
		},
		"activity": func(a, b registry.Session) int {
			return strings.Compare(
				formatActivity(
					a.Activity(),
				),

				formatActivity(
					b.Activity(),
				),
			)
		},
		"cwd": func(a, b registry.Session) int {
			return strings.Compare(
				a.CWD,
				b.CWD,
			)
		},
		"id": func(a, b registry.Session) int {
			return strings.Compare(
				a.ID,
				b.ID,
			)
		},
	}[k]; ok {
		return c, nil
	}

	return nil, fmt.Errorf("%w: %q", errInvalidListSort, k)
}

func listSortKey(s string) (string, error) {
	if strings.TrimSpace(s) == "" {
		return "updated", nil
	}

	key, ok := config.SortKey(s)
	if !ok {
		return "", fmt.Errorf("%w: %q", errInvalidListSort, s)
	}

	return key, nil
}

func compareSessionMultiplexer(a registry.Session, b registry.Session) int {
	if comparison := strings.Compare(
		string(a.Location.Kind),
		string(b.Location.Kind),
	); comparison != 0 {
		return comparison
	}

	if comparison := strings.Compare(
		a.Location.SessionName,
		b.Location.SessionName,
	); comparison != 0 {
		return comparison
	}

	if comparison := strings.Compare(
		multiplexerContainerLabel(a.Location),
		multiplexerContainerLabel(b.Location),
	); comparison != 0 {
		return comparison
	}

	if comparison := strings.Compare(a.Location.PaneID, b.Location.PaneID); comparison != 0 {
		return comparison
	}

	return strings.Compare(a.ID, b.ID)
}

func compareSessionTmux(a registry.Session, b registry.Session) int {
	if c := strings.Compare(a.Location.SessionName, b.Location.SessionName); c != 0 {
		return c
	}

	if c := strings.Compare(a.Location.WindowIndex, b.Location.WindowIndex); c != 0 {
		return c
	}

	return strings.Compare(a.ID, b.ID)
}

func compareSessionUpdated(a registry.Session, b registry.Session) int {
	return a.UpdatedAt.Compare(b.UpdatedAt)
}

func compareSessionCreated(a registry.Session, b registry.Session) int {
	return a.CreatedAt.Compare(b.CreatedAt)
}
