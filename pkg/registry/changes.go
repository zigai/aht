package registry

import (
	"cmp"
	"reflect"
	"slices"
	"time"
)

type Change struct {
	ID      string
	Removed bool
}

func stateChanges(before, after State) []Change {
	var changes []Change
	for id, previous := range before.Sessions {
		current, exists := after.Sessions[id]
		if !exists {
			changes = append(changes, Change{ID: id, Removed: true})
			continue
		}
		if !reflect.DeepEqual(consumerState(previous), consumerState(current)) {
			changes = append(changes, Change{ID: id, Removed: false})
		}
	}
	for id := range after.Sessions {
		if _, exists := before.Sessions[id]; !exists {
			changes = append(changes, Change{ID: id, Removed: false})
		}
	}
	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(a.ID, b.ID) })
	return changes
}

func consumerState(session Session) Session {
	var observations Observations
	var incarnation Incarnation
	session.Incarnation = incarnation
	session.Observations = observations
	session.UpdatedAt = time.Time{}
	if decision := session.Decision(); decision != nil {
		value := *decision
		value.ObservedAt = time.Time{}
		session.setDecision(&value)
	}
	return session
}
