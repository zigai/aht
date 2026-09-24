package registry

import (
	"fmt"
)

func validateSnapshot(snap snapshot) error {
	for id, session := range snap.Sessions {
		if reason := storedSessionCorruption(id, session); reason != "" {
			return fmt.Errorf("%w: session %q: %s", ErrCorruptStore, id, reason)
		}
	}
	return nil
}

func storedSessionCorruption(id string, session Session) string {
	if reason := storedSessionIdentityCorruption(id, session); reason != "" {
		return reason
	}
	if reason := storedSessionStateCorruption(session); reason != "" {
		return reason
	}
	return storedObservationCorruption(session.Observations)
}

func storedSessionIdentityCorruption(id string, session Session) string {
	switch {
	case id == "" || session.ID != id:
		return "map key and session id differ"
	case session.SchemaVersion != storeSchemaVersion:
		return "invalid session schema version"
	case session.Harness == "":
		return "invalid harness"
	case session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() || session.UpdatedAt.Before(session.CreatedAt):
		return "invalid session timestamps"
	default:
		return ""
	}
}

func storedSessionStateCorruption(session Session) string {
	if session.Liveness == nil {
		return "missing liveness"
	}
	if activity := session.Activity(); activity != nil && !activity.IsValid() {
		return "invalid activity"
	}
	switch {
	case session.Process != nil && !validStoredProcess(*session.Process, false):
		return "invalid process identity"
	case session.Location.PanePID < 0:
		return "invalid multiplexer pane pid"
	case !session.Location.Empty() && !session.Location.Kind.IsValid():
		return "invalid multiplexer kind"
	case session.Decision() != nil && !validStoredActivityDecision(*session.Decision()):
		return "invalid activity decision"
	default:
		return ""
	}
}

func storedObservationCorruption(observations Observations) string {
	if native := observations.Native; native != nil && !validStoredNativeObservation(*native) {
		return "invalid native observation"
	}
	if process := observations.Process; process != nil && !validStoredProcessObservation(*process) {
		return "invalid process observation"
	}
	if multiplexer := observations.Location; multiplexer != nil && !validStoredMultiplexerObservation(*multiplexer) {
		return "invalid multiplexer observation"
	}
	if catalog := observations.Catalog; catalog != nil && !validStoredCatalogObservation(*catalog) {
		return "invalid catalog observation"
	}
	if screen := observations.Screen; screen != nil && !validStoredScreenObservation(*screen) {
		return "invalid screen observation"
	}
	return ""
}

func validStoredProcessObservation(observation ProcessObservation) bool {
	return !observation.ObservedAt.IsZero() && validStoredProcess(observation.Process, !observation.Present)
}

func validStoredMultiplexerObservation(observation MultiplexerObservation) bool {
	return !observation.ObservedAt.IsZero() &&
		validStoredProcess(observation.Process, false) &&
		observation.Context.PanePID >= 0 &&
		(observation.Context.Empty() || observation.Context.Kind.IsValid())
}

func validStoredCatalogObservation(observation CatalogObservation) bool {
	return !observation.ObservedAt.IsZero() && observation.ProcessPID >= 0
}

func validStoredNativeObservation(observation NativeObservation) bool {
	return !observation.ObservedAt.IsZero() &&
		validStoredProcess(observation.Process, true) &&
		validStoredLifecycle(observation.Lifecycle) &&
		validStoredOptionalPresence(observation.Presence) &&
		validStoredOptionalActivity(observation.Activity)
}

func validStoredScreenObservation(observation ScreenObservation) bool {
	return !observation.ObservedAt.IsZero() &&
		validStoredProcess(observation.Process, false) &&
		observation.Activity.IsValid() &&
		observation.ManifestVersion >= 0
}

func validStoredActivityDecision(decision ActivityDecision) bool {
	return !decision.ObservedAt.IsZero() && validStoredProcess(decision.Process, true) && decision.ManifestVersion >= 0
}

func validStoredLifecycle(lifecycle *NativeLifecycle) bool {
	return lifecycle == nil || lifecycle.IsValid()
}

func validStoredOptionalPresence(presence *Presence) bool {
	return presence == nil || presence.IsValid()
}

func validStoredOptionalActivity(activity *Activity) bool {
	return activity == nil || activity.IsValid()
}

func validStoredProcess(process ProcessIdentity, allowZero bool) bool {
	var zero ProcessIdentity
	if process == zero {
		return allowZero
	}
	return process.Complete() && process.PPID >= 0 && process.ProcessGroupID >= 0
}
