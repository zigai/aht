package registry

import (
	"context"
	"time"
)

const (
	AuthorityHook    Authority = "hook"
	AuthorityScreen  Authority = "screen"
	AuthorityProcess Authority = "process"
)

type Authority string

type Rules interface {
	Known(harness Harness) bool
	Policy(harness Harness) Policy
}

type Policy struct {
	ExclusiveProcess bool
	Authority        Authority
	ScreenFallback   bool
	Reporter         string
	CatalogCreates   bool
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

func (r Reducer) Apply(state State, batch []Observation, receivedAt time.Time) (State, []Change, error) {
	previous := snapshot{JournalSequence: 0, SchemaVersion: storeSchemaVersion, Sessions: state.Sessions, UpdatedAt: state.UpdatedAt}
	candidate := cloneRegistrySnapshot(previous)
	if _, err := r.applyObservationBatch(context.Background(), &candidate, batch, receivedAt); err != nil {
		return state, nil, err
	}
	result := State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt}
	return result, stateChanges(state, result), nil
}

func (a Authority) IsValid() bool {
	switch a {
	case AuthorityHook, AuthorityScreen, AuthorityProcess:
		return true
	}
	return false
}
