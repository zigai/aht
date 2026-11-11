package registry

import (
	"context"
	"time"
)

const (
	AuthorityHook   Authority = "hook"
	AuthorityScreen Authority = "screen"
	// AuthorityTerminal is the state the harness itself reported to its
	// terminal with the OSC 7501 program status protocol.
	AuthorityTerminal Authority = "terminal"
	AuthorityProcess  Authority = "process"
)

type Authority string

type Rules interface {
	Known(harness Harness) bool
	Policy(harness Harness) Policy
	SamePane(a Location, b Location) bool
}

type Policy struct {
	Details              DetailCapabilities
	ExclusiveProcess     bool
	Authority            Authority
	ScreenFallback       bool
	RetainNativeActivity bool
	Reporter             string
	CatalogCreates       bool
	// HeldTurnEvents names the native events that end a turn but still report
	// running while background work keeps the turn open. Such a report
	// outranks a resting terminal record.
	HeldTurnEvents []string
}

type Reducer struct {
	rules Rules
}

type State struct {
	Sessions  map[string]Session
	UpdatedAt time.Time
}

func NewReducer(rules Rules) Reducer {
	return Reducer{rules: rules}
}

func (r Reducer) Apply(
	state State,
	batch []Observation,
	receivedAt time.Time,
) (State, []Change, error) {
	previous := snapshot{
		JournalSequence: 0,
		SchemaVersion:   storeSchemaVersion,
		Sessions:        state.Sessions,
		UpdatedAt:       state.UpdatedAt,
		EndedProcesses:  nil,
	}

	candidate := cloneRegistrySnapshot(previous)
	if _, _, err := r.applyObservationBatch(
		context.Background(),
		&candidate,
		batch,
		receivedAt,
	); err != nil {
		return state, nil, err
	}

	result := State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt}

	return result, stateChanges(state, result), nil
}

func (a Authority) Valid() bool {
	switch a {
	case AuthorityHook, AuthorityScreen, AuthorityTerminal, AuthorityProcess:
		return true
	}

	return false
}
