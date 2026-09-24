package registry

import (
	"bytes"
	"encoding/json"
	"maps"
)

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneAttributes(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	return maps.Clone(value)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return bytes.Clone(value)
}

func activityEqual(left, right *Activity) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func cloneRegistrySnapshotForMutation(source snapshot) snapshot {
	return snapshot{
		SchemaVersion:   source.SchemaVersion,
		JournalSequence: source.JournalSequence,
		UpdatedAt:       source.UpdatedAt,
		// The reducer treats Session values as copy-on-write and replaces every
		// nested pointer or slice it changes, so cloning the map is sufficient
		// for atomic rollback without copying every unaffected session.
		Sessions: maps.Clone(source.Sessions),
	}
}

func cloneRegistrySnapshot(source snapshot) snapshot {
	cloned := snapshot{
		SchemaVersion:   source.SchemaVersion,
		JournalSequence: source.JournalSequence,
		UpdatedAt:       source.UpdatedAt,
		Sessions:        make(map[string]Session, len(source.Sessions)),
	}
	for id, session := range source.Sessions {
		cloned.Sessions[id] = cloneSessionValue(session)
	}

	return cloned
}

func cloneSessionValue(source Session) Session {
	cloned := source
	cloned.setActivity(clonePtr(source.Activity()))
	cloned.ResumeCommand = append([]string(nil), source.ResumeCommand...)
	if source.Process != nil {
		process := *source.Process
		cloned.Process = &process
	}
	cloned.Observations = cloneObservations(source.Observations)
	if source.Decision() != nil {
		decision := *source.Decision()
		cloned.setDecision(&decision)
	}

	return cloned
}

func cloneObservations(source Observations) Observations {
	var cloned Observations
	if source.Native != nil {
		native := *source.Native
		native.Lifecycle = clonePtr(source.Native.Lifecycle)
		native.Presence = clonePtr(source.Native.Presence)
		native.Activity = clonePtr(source.Native.Activity)
		native.Reporter.Sequence = clonePtr(source.Native.Reporter.Sequence)
		native.Attributes = cloneAttributes(source.Native.Attributes)
		native.RawPayload = cloneRaw(source.Native.RawPayload)
		cloned.Native = &native
	}
	if source.Process != nil {
		process := *source.Process
		cloned.Process = &process
	}
	if source.Location != nil {
		multiplexer := *source.Location
		cloned.Location = &multiplexer
	}
	if source.Catalog != nil {
		catalog := *source.Catalog
		catalog.ResumeCommand = append([]string(nil), source.Catalog.ResumeCommand...)
		cloned.Catalog = &catalog
	}
	if source.Screen != nil {
		screen := *source.Screen
		cloned.Screen = &screen
	}

	return cloned
}
