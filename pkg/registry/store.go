package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	storeSchemaVersion      = 2
	maxObservedAtFutureSkew = 5 * time.Minute
	automaticGoneRetention  = 5 * time.Minute
	maxSnapshotBytes        = 64 << 20

	// IntegrationActivityLease is the maximum age of a matching integration
	// transition before multiplexer screen evidence becomes authoritative again.
	IntegrationActivityLease = 30 * time.Second
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrHarnessRequired     = errors.New("harness is required")
	ErrObservationIdentity = errors.New("observation requires identity")
	ErrObservationConflict = errors.New("observation conflicts with accepted evidence")
	ErrCorruptStore        = errors.New("corrupt registry store")
	ErrStoreTooLarge       = errors.New("registry store exceeds size limit")

	_ Store = (*FileStore)(nil)
)

type UnsupportedSchemaError struct {
	Path    string
	Version int
}

type snapshot struct {
	SchemaVersion int                `json:"schema_version"`
	LegacyVersion *int               `json:"version,omitempty"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Sessions      map[string]Session `json:"sessions"`
}

type GCResult struct {
	Deleted   int `json:"deleted"`
	Remaining int `json:"remaining"`
}

type ResetResult struct {
	Cleared   int `json:"cleared"`
	Remaining int `json:"remaining"`
}

type FileStore struct {
	path             string
	now              func() time.Time
	onLockContention func()
}

type summaryKey struct {
	kind   MultiplexerKind
	server string
	id     string
	name   string
}

func (e *UnsupportedSchemaError) Error() string {
	version := "missing"
	if e.Version != 0 {
		version = strconv.Itoa(e.Version)
	}
	return fmt.Sprintf("unsupported store schema %s at %s; run aht --store %s manage state reset --force or move/remove the file", version, e.Path, e.Path)
}

func NewFileStore(path string) *FileStore {
	if path == "" {
		path = DefaultStorePath()
	}
	return &FileStore{path: path, now: func() time.Time { return time.Now().UTC() }, onLockContention: nil}
}

func (s *FileStore) Path() string { return s.path }

func (s *FileStore) Observe(ctx context.Context, observation Observation) (Session, error) {
	sessions, err := s.ObserveBatch(ctx, []Observation{observation})
	if err != nil {
		return Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	snap, loadErr := s.load()
	if loadErr != nil {
		return Session{}, loadErr
	}
	id := findMatchingSession(snap.Sessions, observation)
	if id == "" {
		id = sessionIDForObservation(observation)
	}
	return s.Get(ctx, id)
}

func (s *FileStore) ObserveBatch(ctx context.Context, observations []Observation) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking context: %w", err)
	}

	receivedAt := s.now().UTC()
	var saved []Session
	err := s.withSnapshot(ctx, func(snap *snapshot) error {
		var err error
		saved, err = applyObservationBatch(ctx, snap, observations, receivedAt)

		return err
	})
	if err != nil {
		return nil, err
	}

	return saved, nil
}

//nolint:gocognit,cyclop // one transaction preserves identity and evidence precedence.
func applyObservationBatch(
	ctx context.Context,
	snap *snapshot,
	observations []Observation,
	receivedAt time.Time,
) ([]Session, error) {
	saved := make([]Session, 0, len(observations))
	for index := range observations {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("checking context: %w", err)
		}

		observation := observations[index]
		if observation.ObservedAt.IsZero() {
			observation.ObservedAt = receivedAt
		}
		if err := observation.Validate(); err != nil {
			return nil, err
		}

		at := observationTime(observation.ObservedAt, receivedAt)
		retireConflictingProcessSessions(snap.Sessions, observation, at, receivedAt)
		id := findAndReconcileMatchingSession(snap.Sessions, observation)
		if id == "" &&
			observation.Source == ObservationSourceCatalog &&
			(observation.Harness != HarnessClaude || observation.Catalog == nil || !observation.Catalog.Current) {
			continue
		}
		if id == "" {
			id = sessionIDForObservation(observation)
		}

		session := snap.Sessions[id]
		if session.ID == "" {
			session = newSession(id, observation.Harness, receivedAt)
		} else if session.Harness != observation.Harness {
			id = sessionIDForObservation(observation)
			session = newSession(id, observation.Harness, receivedAt)
		}
		sequencedAt, sequenceErr := sequencedObservationTime(session, observation, at)
		if sequenceErr != nil {
			return nil, sequenceErr
		}
		at = sequencedAt
		if shouldIgnoreNativeAfterGone(session, observation, at) {
			saved = append(saved, session)
			continue
		}
		if err := applyObservation(&session, observation, at, receivedAt); err != nil {
			return nil, err
		}

		snap.Sessions[session.ID] = session
		saved = append(saved, session)
	}

	deleteExpiredGoneSessions(
		snap.Sessions,
		receivedAt,
		automaticGoneRetention,
		func(session Session) time.Time { return session.UpdatedAt },
	)
	snap.UpdatedAt = maxTime(snap.UpdatedAt, receivedAt)

	return saved, nil
}

func newSession(id string, harness Harness, now time.Time) Session {
	activity := ActivityUnknown
	return Session{
		SchemaVersion:     storeSchemaVersion,
		ID:                id,
		Harness:           harness,
		Presence:          PresenceUnknown,
		Activity:          &activity,
		SessionID:         "",
		SessionPath:       "",
		ResumeCommand:     nil,
		CWD:               "",
		ProjectRoot:       "",
		Process:           nil,
		Tmux:              TmuxContext{},        //nolint:exhaustruct_v5 // new sessions have no location
		Multiplexer:       MultiplexerContext{}, //nolint:exhaustruct_v5 // new sessions have no location
		Observations:      Observations{},       //nolint:exhaustruct_v5 // no evidence yet
		CreatedAt:         now,
		UpdatedAt:         now,
		PresenceChangedAt: time.Time{},
		ActivityChangedAt: time.Time{},
		ActivityDecision:  nil,
	}
}

func observationTime(observedAt, receivedAt time.Time) time.Time {
	if observedAt.IsZero() {
		return receivedAt
	}
	observedAt = observedAt.UTC()
	if observedAt.After(receivedAt.Add(maxObservedAtFutureSkew)) {
		return receivedAt
	}
	return observedAt
}

//nolint:cyclop // each source owns an independent timestamp slot
func sourceSlotTime(session Session, observation Observation) time.Time {
	switch observation.Source {
	case ObservationSourceNative:
		if session.Observations.Native != nil {
			return session.Observations.Native.ObservedAt
		}
	case ObservationSourceProcess:
		if session.Observations.Process != nil {
			return session.Observations.Process.ObservedAt
		}
	case ObservationSourceTmux:
		if session.Observations.Tmux != nil {
			return session.Observations.Tmux.ObservedAt
		}
	case ObservationSourceMultiplexer:
		if session.Observations.Multiplexer != nil {
			return session.Observations.Multiplexer.ObservedAt
		}
	case ObservationSourceCatalog:
		if session.Observations.Catalog != nil {
			return session.Observations.Catalog.ObservedAt
		}
	case ObservationSourceScreen:
		if session.Observations.Screen != nil {
			return session.Observations.Screen.ObservedAt
		}
	}
	return time.Time{}
}

func currentProcessObservationTime(session Session) time.Time {
	if session.Process == nil {
		return time.Time{}
	}
	var latest time.Time
	if observation := session.Observations.Native; observation != nil && observation.Process.Equal(*session.Process) {
		latest = maxTime(latest, observation.ObservedAt)
	}
	if observation := session.Observations.Process; observation != nil && observation.Process.Equal(*session.Process) {
		latest = maxTime(latest, observation.ObservedAt)
	}
	if observation := session.Observations.Tmux; observation != nil && observation.Process.Equal(*session.Process) {
		latest = maxTime(latest, observation.ObservedAt)
	}
	if observation := session.Observations.Multiplexer; observation != nil && observation.Process.Equal(*session.Process) {
		latest = maxTime(latest, observation.ObservedAt)
	}
	if observation := session.Observations.Screen; observation != nil && observation.Process.Equal(*session.Process) {
		latest = maxTime(latest, observation.ObservedAt)
	}
	return latest
}

func existingSlot(session Session, observation Observation) any {
	switch observation.Source {
	case ObservationSourceNative:
		return session.Observations.Native
	case ObservationSourceProcess:
		return session.Observations.Process
	case ObservationSourceTmux:
		return session.Observations.Tmux
	case ObservationSourceMultiplexer:
		return session.Observations.Multiplexer
	case ObservationSourceCatalog:
		return session.Observations.Catalog
	case ObservationSourceScreen:
		return session.Observations.Screen
	default:
		return nil
	}
}

func sequencedObservationTime(
	session Session,
	observation Observation,
	at time.Time,
) (time.Time, error) {
	if observation.Source != ObservationSourceNative {
		return at, nil
	}

	reporter := strings.TrimSpace(observation.Attributes["aht_integration"])
	previous := session.Observations.Native
	if reporter == "" ||
		previous == nil ||
		strings.TrimSpace(previous.Attributes["aht_integration"]) != reporter {
		return at, nil
	}
	if previous.Sequence != nil && observation.Sequence == nil {
		return time.Time{}, fmt.Errorf(
			"%w: reporter %q omitted sequence after using sequence %d",
			ErrObservationConflict,
			reporter,
			*previous.Sequence,
		)
	}
	if observation.Sequence == nil {
		return at, nil
	}
	if previous.Sequence != nil && *observation.Sequence <= *previous.Sequence {
		return time.Time{}, fmt.Errorf(
			"%w: reporter %q sequence %d does not follow %d",
			ErrObservationConflict,
			reporter,
			*observation.Sequence,
			*previous.Sequence,
		)
	}
	if !at.After(previous.ObservedAt) {
		at = previous.ObservedAt.Add(time.Nanosecond)
	}

	return at, nil
}

func shouldIgnoreNativeAfterGone(session Session, observation Observation, at time.Time) bool {
	if observation.Source != ObservationSourceNative || session.Presence != PresenceGone {
		return false
	}
	if observation.Lifecycle != nil && (*observation.Lifecycle == NativeLifecycleStart || *observation.Lifecycle == NativeLifecycleResume) {
		return !at.After(session.PresenceChangedAt)
	}
	return true
}

func applyObservation(session *Session, observation Observation, at, receivedAt time.Time) error {
	if err := validateIncomingProcessTime(*session, observation, at); err != nil {
		return err
	}
	if previous := sourceSlotTime(*session, observation); !previous.IsZero() {
		if at.Before(previous) {
			return fmt.Errorf("%w: %s observation at %s precedes %s", ErrObservationConflict, observation.Source, at, previous)
		}
		if at.Equal(previous) {
			if observationEquivalent(*session, observation, at) {
				return nil
			}
			return fmt.Errorf("%w: %s observation at %s", ErrObservationConflict, observation.Source, at)
		}
	}
	previousPresence := session.Presence
	previousActivity := session.Activity
	if err := storeObservation(session, observation, at); err != nil {
		return err
	}
	applyIdentity(session, observation)
	applyMetadata(session, observation, at)
	applyPresenceAndActivity(session, observation, at)
	session.SchemaVersion = storeSchemaVersion
	session.UpdatedAt = maxTime(session.UpdatedAt, receivedAt)
	if session.Presence != previousPresence {
		session.PresenceChangedAt = at
	}
	if !activityEqual(session.Activity, previousActivity) {
		session.ActivityChangedAt = at
	}
	return nil
}

func validateIncomingProcessTime(session Session, observation Observation, at time.Time) error {
	if observation.Process == nil || session.Process == nil || session.Process.Equal(*observation.Process) {
		return nil
	}
	currentAt := currentProcessObservationTime(session)
	if currentAt.IsZero() || !at.Before(currentAt) {
		return nil
	}
	return fmt.Errorf("%w: process identity observation at %s precedes current process at %s", ErrObservationConflict, at, currentAt)
}

func observationEquivalent(session Session, observation Observation, at time.Time) bool {
	candidate := session
	if err := storeObservation(&candidate, observation, at); err != nil {
		return false
	}
	return reflect.DeepEqual(existingSlot(session, observation), existingSlot(candidate, observation))
}

func storeObservation(session *Session, observation Observation, at time.Time) error {
	switch observation.Source {
	case ObservationSourceNative:
		storeNativeObservation(session, observation, at)
	case ObservationSourceProcess:
		storeProcessObservation(session, observation, at)
	case ObservationSourceTmux:
		return storeTmuxObservation(session, observation, at)
	case ObservationSourceMultiplexer:
		return storeMultiplexerObservation(session, observation, at)
	case ObservationSourceCatalog:
		return storeCatalogObservation(session, observation, at)
	case ObservationSourceScreen:
		return storeScreenObservation(session, observation, at)
	default:
		return ErrUnknownSource
	}
	return nil
}

func storeNativeObservation(session *Session, observation Observation, at time.Time) {
	var process ProcessIdentity
	if observation.Process != nil {
		process = *observation.Process
	}
	session.Observations.Native = &NativeObservation{Event: observation.NativeEvent, Lifecycle: clonePtr(observation.Lifecycle), Presence: clonePtr(observation.Presence), Activity: clonePtr(observation.Activity), ActivityAuthoritative: clonePtr(observation.ActivityAuthoritative), Sequence: clonePtr(observation.Sequence), SessionID: observation.Identity.SessionID, SessionPath: observation.Identity.SessionPath, ObservedAt: at, Attributes: cloneAttributes(observation.Attributes), RawPayload: cloneRaw(observation.RawPayload), Process: process}
}

func storeProcessObservation(session *Session, observation Observation, at time.Time) {
	present := observation.ProcessPresent != nil && *observation.ProcessPresent
	var process ProcessIdentity
	if observation.Process != nil {
		process = *observation.Process
	}
	session.Observations.Process = &ProcessObservation{Present: present, Process: process, ObservedAt: at}
}

func storeTmuxObservation(session *Session, observation Observation, at time.Time) error {
	if observation.Tmux == nil || observation.Process == nil {
		return ErrInvalidObservation
	}
	session.Observations.Tmux = &TmuxObservation{Process: *observation.Process, Context: *observation.Tmux, ObservedAt: at}
	return nil
}

func storeMultiplexerObservation(session *Session, observation Observation, at time.Time) error {
	if observation.Multiplexer == nil || observation.Process == nil {
		return ErrInvalidObservation
	}
	session.Observations.Multiplexer = &MultiplexerObservation{Process: *observation.Process, Context: *observation.Multiplexer, ObservedAt: at}
	return nil
}

func storeCatalogObservation(session *Session, observation Observation, at time.Time) error {
	if observation.Catalog == nil {
		return ErrInvalidObservation
	}
	session.Observations.Catalog = &CatalogObservation{SessionID: observation.Identity.SessionID, SessionPath: observation.Identity.SessionPath, ResumeCommand: append([]string(nil), observation.Catalog.ResumeCommand...), CWD: observation.Catalog.CWD, ProjectRoot: observation.Catalog.ProjectRoot, ProcessPID: observation.Catalog.ProcessPID, ObservedAt: at}
	return nil
}

func storeScreenObservation(session *Session, observation Observation, at time.Time) error {
	if observation.Screen == nil || observation.Process == nil || observation.Activity == nil {
		return ErrInvalidObservation
	}
	screen := *observation.Screen
	screen.Activity = *observation.Activity
	screen.Process = *observation.Process
	screen.ObservedAt = at
	session.Observations.Screen = &screen
	return nil
}

func applyIdentity(session *Session, observation Observation) {
	if observation.Identity.SessionID != "" {
		session.SessionID = observation.Identity.SessionID
	}
	if observation.Identity.SessionPath != "" {
		session.SessionPath = filepath.Clean(observation.Identity.SessionPath)
	}
}

//nolint:cyclop // metadata dimensions are reduced independently
func applyMetadata(session *Session, observation Observation, at time.Time) {
	if observation.Process != nil && observation.Process.Complete() {
		process := *observation.Process
		if session.Process != nil && !session.Process.Equal(process) {
			session.Activity = new(ActivityUnknown)
			session.ActivityDecision = &ActivityDecision{Authority: "process", Reason: "process_replaced", RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "", Process: process, ObservedAt: at}
			session.Observations.Screen = nil
		}
		session.Process = &process
		if process.CWD != "" {
			session.CWD = process.CWD
		}
		if observation.Source == ObservationSourceProcess {
			return
		}
	}
	if (observation.Source == ObservationSourceNative || observation.Source == ObservationSourceCatalog) && observation.Catalog != nil {
		applyCatalogMetadata(session, observation.Catalog)
	}
	if observation.Tmux != nil {
		session.Tmux = *observation.Tmux
		session.Multiplexer = MultiplexerFromTmux(*observation.Tmux)
		if session.CWD == "" {
			session.CWD = observation.Tmux.PaneCurrentPath
		}
	}
	if observation.Multiplexer != nil {
		session.Multiplexer = *observation.Multiplexer
		session.Tmux = observation.Multiplexer.TmuxContext()
		if session.CWD == "" {
			session.CWD = observation.Multiplexer.PaneCurrentPath
		}
	}
}

func applyCatalogMetadata(session *Session, catalog *CatalogMetadata) {
	if session.CWD == "" {
		session.CWD = catalog.CWD
	}
	if session.ProjectRoot == "" {
		session.ProjectRoot = catalog.ProjectRoot
	}
	if len(session.ResumeCommand) == 0 {
		session.ResumeCommand = append([]string(nil), catalog.ResumeCommand...)
	}
}

//nolint:gocognit,cyclop // presence and activity are reduced independently by source precedence
func applyPresenceAndActivity(session *Session, observation Observation, at time.Time) {
	switch observation.Source {
	case ObservationSourceNative:
		native := session.Observations.Native
		if native == nil {
			return
		}
		if native.Lifecycle != nil {
			switch *native.Lifecycle {
			case NativeLifecycleEnd:
				setGone(session, at)
			case NativeLifecycleStart, NativeLifecycleResume:
				if session.Presence == PresenceGone && at.After(session.PresenceChangedAt) {
					session.Presence = PresenceUnknown
					session.Activity = new(ActivityUnknown)
				}
			}
		}
		if native.Presence != nil {
			switch *native.Presence {
			case PresenceGone:
				setGone(session, at)
			case PresenceLive:
				if !hasNativeEnd(session) && !at.Before(session.PresenceChangedAt) {
					session.Presence = PresenceLive
				}
			case PresenceUnknown:
				if !hasNativeEnd(session) && !at.Before(session.PresenceChangedAt) {
					session.Presence = PresenceUnknown
				}
			}
		}
		if native.Activity != nil && activityIsAuthoritative(observation) && session.Presence != PresenceGone && at.After(session.PresenceChangedAt) && (session.ActivityDecision == nil || !at.Before(session.ActivityDecision.ObservedAt)) {
			session.Activity = clonePtr(native.Activity)
			session.ActivityDecision = &ActivityDecision{Authority: "hook", Reason: native.Event, RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "", Process: native.Process, ObservedAt: at}
		}
	case ObservationSourceProcess:
		process := session.Observations.Process
		if process == nil {
			return
		}
		if process.Present {
			if !hasNativeEnd(session) && !at.Before(session.PresenceChangedAt) {
				session.Presence = PresenceLive
				if session.Activity == nil {
					session.Activity = new(ActivityUnknown)
				}
			}
			return
		}
		setGone(session, at)
	case ObservationSourceScreen:
		screen := session.Observations.Screen
		if screen == nil || observation.Process == nil || session.Presence == PresenceGone || !screen.Process.Equal(*observation.Process) {
			return
		}
		if screenFallbackSuperseded(*session, *screen) {
			return
		}
		session.Activity = new(screen.Activity)
		session.ActivityDecision = &ActivityDecision{Authority: screen.Authority, Reason: screen.Reason, RuleID: screen.RuleID, ManifestSource: screen.ManifestSource, ManifestVersion: screen.ManifestVersion, FallbackReason: screen.FallbackReason, Process: screen.Process, ObservedAt: at}
	case ObservationSourceTmux, ObservationSourceMultiplexer, ObservationSourceCatalog:
		return
	}
}

func activityIsAuthoritative(observation Observation) bool {
	return observation.ActivityAuthoritative == nil || *observation.ActivityAuthoritative
}

func screenFallbackSuperseded(session Session, screen ScreenObservation) bool {
	if screen.FallbackForIntegration == "" || session.Observations.Native == nil {
		return false
	}
	native := session.Observations.Native
	if native.Attributes["aht_integration"] != screen.FallbackForIntegration || !native.Process.Equal(screen.Process) || native.Activity == nil || *native.Activity == ActivityUnknown {
		return false
	}
	if native.Presence != nil && *native.Presence == PresenceGone {
		return false
	}
	if screen.ObservedAt.Sub(native.ObservedAt) > IntegrationActivityLease {
		return false
	}
	return native.Lifecycle == nil || *native.Lifecycle != NativeLifecycleEnd
}

func hasNativeEnd(session *Session) bool {
	return session.Observations.Native != nil && session.Observations.Native.Lifecycle != nil && *session.Observations.Native.Lifecycle == NativeLifecycleEnd
}

func setGone(session *Session, at time.Time) {
	if at.Before(session.PresenceChangedAt) {
		return
	}
	if session.Presence != PresenceGone {
		session.Presence = PresenceGone
		session.PresenceChangedAt = at
	}
	if session.Activity != nil {
		session.Activity = nil
		session.ActivityChangedAt = at
	}
	var process ProcessIdentity
	if session.Process != nil {
		process = *session.Process
	}
	session.ActivityDecision = &ActivityDecision{Authority: "process", Reason: "process_gone", RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "", Process: process, ObservedAt: at}
}

func retireConflictingProcessSessions(
	sessions map[string]Session,
	observation Observation,
	at time.Time,
	receivedAt time.Time,
) {
	if observation.Process == nil || !observation.Process.Complete() {
		return
	}

	for id, session := range sessions {
		replace, nativeReplacement := conflictingSessionReplacement(session, observation)
		if !replace {
			continue
		}
		if currentAt := currentProcessObservationTime(session); !currentAt.IsZero() && at.Before(currentAt) {
			continue
		}
		if session.Presence != PresenceGone {
			setGone(&session, at)
			if nativeReplacement {
				session.ActivityDecision = &ActivityDecision{
					Authority: "hook", Reason: "session_replaced", RuleID: "", ManifestSource: "",
					ManifestVersion: 0, FallbackReason: "", Process: *observation.Process, ObservedAt: at,
				}
			}
		}
		session.UpdatedAt = maxTime(session.UpdatedAt, receivedAt)
		sessions[id] = session
	}
}

func conflictingSessionReplacement(session Session, observation Observation) (bool, bool) {
	sameProcess := session.Process != nil && session.Process.Equal(*observation.Process)
	if nativeSessionReplacement(session, observation, sameProcess) {
		return true, true
	}
	if session.Harness == observation.Harness || session.Presence != PresenceLive {
		return false, false
	}
	if processHarnessReplacement(observation, sameProcess) {
		return true, false
	}
	if session.Process == nil && observation.Source == ObservationSourceTmux && sameTmuxPane(session, observation.Tmux) {
		return true, false
	}
	return false, false
}

func nativeSessionReplacement(session Session, observation Observation, sameProcess bool) bool {
	return observation.Source == ObservationSourceNative &&
		observationHasIdentity(observation) &&
		session.Harness == observation.Harness &&
		observationIdentityConflicts(session, observation.Identity) &&
		sameProcess
}

func processHarnessReplacement(observation Observation, sameProcess bool) bool {
	return observation.Source == ObservationSourceProcess &&
		observation.ProcessPresent != nil &&
		*observation.ProcessPresent &&
		sameProcess
}

func sameTmuxPane(session Session, observed *TmuxContext) bool {
	if observed == nil || observed.PaneID == "" {
		return false
	}
	current := session.Tmux
	if current.Empty() && session.Multiplexer.Kind == MultiplexerTmux {
		current = session.Multiplexer.TmuxContext()
	}
	if current.PaneID != observed.PaneID {
		return false
	}
	if current.ServerSocket != "" || observed.ServerSocket != "" {
		return current.ServerSocket == observed.ServerSocket
	}
	if current.SessionID != "" || observed.SessionID != "" {
		return current.SessionID == observed.SessionID
	}
	return false
}

func observationHasIdentity(observation Observation) bool {
	return observation.Identity.SessionID != "" || observation.Identity.SessionPath != ""
}

func observationIdentityConflicts(session Session, identity ObservationIdentity) bool {
	if identity.SessionID != "" && session.SessionID != "" {
		return identity.SessionID != session.SessionID
	}
	if identity.SessionPath != "" && session.SessionPath != "" {
		return filepath.Clean(identity.SessionPath) != filepath.Clean(session.SessionPath)
	}
	return false
}

func findAndReconcileMatchingSession(sessions map[string]Session, observation Observation) string {
	identityID := findIdentityMatchingSession(sessions, observation)
	processID := findProcessMatchingSession(sessions, observation)
	if identityID != "" && processID != "" && identityID != processID {
		provisional := sessions[identityID]
		if provisional.Process == nil {
			target := mergeProvisionalSession(sessions[processID], provisional)
			sessions[processID] = target
			delete(sessions, identityID)
			return processID
		}
	}
	if processID != "" {
		return processID
	}
	if identityID != "" {
		return identityID
	}
	if observation.Source == ObservationSourceCatalog && observation.Catalog != nil && observation.Catalog.ProcessPID > 0 {
		return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.Process != nil && session.Process.PID == observation.Catalog.ProcessPID
		})
	}
	return ""
}

func findMatchingSession(sessions map[string]Session, observation Observation) string {
	if id := findProcessMatchingSession(sessions, observation); id != "" {
		return id
	}
	if id := findIdentityMatchingSession(sessions, observation); id != "" {
		return id
	}
	if observation.Source == ObservationSourceCatalog && observation.Catalog != nil && observation.Catalog.ProcessPID > 0 {
		return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.Process != nil && session.Process.PID == observation.Catalog.ProcessPID
		})
	}
	return ""
}

func findProcessMatchingSession(sessions map[string]Session, observation Observation) string {
	if observation.Process == nil || !observation.Process.Complete() {
		return ""
	}
	return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
		return session.Process != nil &&
			session.Process.Equal(*observation.Process) &&
			!observationIdentityConflicts(session, observation.Identity)
	})
}

func findIdentityMatchingSession(sessions map[string]Session, observation Observation) string {
	if observation.Identity.SessionID != "" {
		if id := bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.SessionID == observation.Identity.SessionID
		}); id != "" {
			return id
		}
	}
	if observation.Identity.SessionPath != "" {
		cleanPath := filepath.Clean(observation.Identity.SessionPath)
		if id := bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.SessionPath != "" && filepath.Clean(session.SessionPath) == cleanPath
		}); id != "" {
			return id
		}
	}
	return ""
}

func bestMatchingSessionID(sessions map[string]Session, harness Harness, match func(Session) bool) string {
	bestID := ""
	var best Session
	for id, session := range sessions {
		if session.Harness != harness || !match(session) {
			continue
		}
		if bestID == "" || sessionMatchPreferred(session, id, best, bestID) {
			bestID, best = id, session
		}
	}
	return bestID
}

func sessionMatchPreferred(candidate Session, candidateID string, current Session, currentID string) bool {
	if candidateRank := presenceMatchRank(candidate.Presence); candidateRank != presenceMatchRank(current.Presence) {
		return candidateRank > presenceMatchRank(current.Presence)
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return candidateID < currentID
}

func presenceMatchRank(presence Presence) int {
	const (
		goneMatchRank = iota
		unknownMatchRank
		liveMatchRank
	)
	switch presence {
	case PresenceLive:
		return liveMatchRank
	case PresenceUnknown:
		return unknownMatchRank
	case PresenceGone:
		return goneMatchRank
	default:
		return -1
	}
}

//nolint:cyclop // each independent metadata dimension is merged only when missing
func mergeProvisionalSession(target Session, provisional Session) Session {
	if target.SessionID == "" {
		target.SessionID = provisional.SessionID
	}
	if target.SessionPath == "" {
		target.SessionPath = provisional.SessionPath
	}
	if len(target.ResumeCommand) == 0 {
		target.ResumeCommand = append([]string(nil), provisional.ResumeCommand...)
	}
	if target.CWD == "" {
		target.CWD = provisional.CWD
	}
	if target.ProjectRoot == "" {
		target.ProjectRoot = provisional.ProjectRoot
	}
	if target.Tmux.Empty() {
		target.Tmux = provisional.Tmux
	}
	if target.Multiplexer.Empty() {
		target.Multiplexer = provisional.Multiplexer
	}
	if target.Presence == PresenceUnknown && provisional.Presence != PresenceUnknown {
		target.Presence = provisional.Presence
		target.PresenceChangedAt = provisional.PresenceChangedAt
	}
	if activityUnknown(target.Activity) && !activityUnknown(provisional.Activity) {
		target.Activity = clonePtr(provisional.Activity)
		target.ActivityChangedAt = provisional.ActivityChangedAt
		target.ActivityDecision = provisional.ActivityDecision
	}
	target.Observations = mergeObservations(target.Observations, provisional.Observations)
	if target.CreatedAt.IsZero() || (!provisional.CreatedAt.IsZero() && provisional.CreatedAt.Before(target.CreatedAt)) {
		target.CreatedAt = provisional.CreatedAt
	}
	target.UpdatedAt = maxTime(target.UpdatedAt, provisional.UpdatedAt)
	return target
}

func activityUnknown(activity *Activity) bool {
	return activity == nil || *activity == ActivityUnknown
}

//nolint:cyclop // each evidence source has an independent newest-observation slot
func mergeObservations(target Observations, provisional Observations) Observations {
	if target.Native == nil || (provisional.Native != nil && provisional.Native.ObservedAt.After(target.Native.ObservedAt)) {
		target.Native = provisional.Native
	}
	if target.Process == nil || (provisional.Process != nil && provisional.Process.ObservedAt.After(target.Process.ObservedAt)) {
		target.Process = provisional.Process
	}
	if target.Tmux == nil || (provisional.Tmux != nil && provisional.Tmux.ObservedAt.After(target.Tmux.ObservedAt)) {
		target.Tmux = provisional.Tmux
	}
	if target.Multiplexer == nil || (provisional.Multiplexer != nil && provisional.Multiplexer.ObservedAt.After(target.Multiplexer.ObservedAt)) {
		target.Multiplexer = provisional.Multiplexer
	}
	if target.Catalog == nil || (provisional.Catalog != nil && provisional.Catalog.ObservedAt.After(target.Catalog.ObservedAt)) {
		target.Catalog = provisional.Catalog
	}
	if target.Screen == nil || (provisional.Screen != nil && provisional.Screen.ObservedAt.After(target.Screen.ObservedAt)) {
		target.Screen = provisional.Screen
	}
	return target
}

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

func (s *FileStore) List(ctx context.Context, filter Filter) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking context: %w", err)
	}
	sessions, _, err := s.watchSnapshot(filter)
	return sessions, err
}

func (s *FileStore) Get(ctx context.Context, id string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, fmt.Errorf("checking context: %w", err)
	}
	snap, err := s.load()
	if err != nil {
		return Session{}, err
	}
	session, ok := snap.Sessions[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	session.SchemaVersion = storeSchemaVersion
	populateMultiplexerProjection(&session)
	return session, nil
}

func populateMultiplexerProjection(session *Session) {
	if session.Multiplexer.Empty() && !session.Tmux.Empty() {
		session.Multiplexer = MultiplexerFromTmux(session.Tmux)
	}
}

func (s *FileStore) SummaryByTmuxSession(ctx context.Context, filter Filter) ([]Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking context: %w", err)
	}
	snap, err := s.load()
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, 0, len(snap.Sessions))
	for _, session := range snap.Sessions {
		populateMultiplexerProjection(&session)
		sessions = append(sessions, session)
	}
	return summariesForSessions(filterSessions(sessions, filter)), nil
}

func summariesForSessions(sessions []Session) []Summary {
	byKey := make(map[summaryKey]*Summary)
	order := make([]summaryKey, 0)
	for _, session := range sessions {
		populateMultiplexerProjection(&session)
		key := summaryKeyForSession(session)
		summary := byKey[key]
		if summary == nil {
			summary = &Summary{
				MultiplexerKind: session.Multiplexer.Kind, MultiplexerSessionID: session.Multiplexer.SessionID,
				MultiplexerServerID:    session.Multiplexer.ServerID,
				MultiplexerSessionName: session.Multiplexer.SessionName,
				TmuxSessionID:          session.Tmux.SessionID, TmuxSessionName: session.Tmux.SessionName,
				Total: 0, Live: 0, Gone: 0, PresenceUnknown: 0,
				Running: 0, Waiting: 0, Idle: 0, Failed: 0, Interrupted: 0, ActivityUnknown: 0,
			}
			byKey[key] = summary
			order = append(order, key)
		}
		summary.addSession(session)
	}
	result := make([]Summary, 0, len(order))
	for _, key := range order {
		result = append(result, *byKey[key])
	}
	return result
}

func (s *Summary) addSession(session Session) {
	s.Total++
	switch session.Presence {
	case PresenceLive:
		s.Live++
	case PresenceGone:
		s.Gone++
	case PresenceUnknown:
		s.PresenceUnknown++
	}
	if session.Presence == PresenceGone {
		return
	}
	switch {
	case session.Activity == nil, *session.Activity == ActivityUnknown:
		s.ActivityUnknown++
	case *session.Activity == ActivityRunning:
		s.Running++
	case *session.Activity == ActivityWaiting:
		s.Waiting++
	case *session.Activity == ActivityIdle:
		s.Idle++
	case *session.Activity == ActivityFailed:
		s.Failed++
	case *session.Activity == ActivityInterrupted:
		s.Interrupted++
	}
}

func summaryKeyForSession(session Session) summaryKey {
	populateMultiplexerProjection(&session)
	key := summaryKey{kind: session.Multiplexer.Kind, server: session.Multiplexer.ServerID, id: "", name: ""}
	if session.Multiplexer.SessionID != "" {
		key.id = session.Multiplexer.SessionID
		return key
	}
	key.name = session.Multiplexer.SessionName
	return key
}

func (s *FileStore) GC(ctx context.Context, deleteAfter time.Duration) (GCResult, error) {
	if err := ctx.Err(); err != nil {
		return GCResult{}, fmt.Errorf("checking context: %w", err)
	}
	now := s.now().UTC()
	result := GCResult{Deleted: 0, Remaining: 0}
	err := s.withSnapshot(ctx, func(snap *snapshot) error {
		result.Deleted = deleteExpiredGoneSessions(
			snap.Sessions,
			now,
			deleteAfter,
			func(session Session) time.Time { return session.PresenceChangedAt },
		)
		result.Remaining = len(snap.Sessions)
		snap.UpdatedAt = now
		return nil
	})
	if err != nil {
		return GCResult{}, err
	}
	return result, nil
}

func deleteExpiredGoneSessions(
	sessions map[string]Session,
	now time.Time,
	deleteAfter time.Duration,
	changedAt func(Session) time.Time,
) int {
	if deleteAfter < 0 {
		return 0
	}

	deleted := 0
	for id, session := range sessions {
		at := changedAt(session)
		if session.Presence != PresenceGone || at.IsZero() || now.Sub(at) < deleteAfter {
			continue
		}

		delete(sessions, id)
		deleted++
	}

	return deleted
}

func (s *FileStore) Reset(ctx context.Context) (ResetResult, error) {
	if err := ctx.Err(); err != nil {
		return ResetResult{}, fmt.Errorf("checking context: %w", err)
	}
	now := s.now().UTC()
	result := ResetResult{Cleared: 0, Remaining: 0}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return ResetResult{}, fmt.Errorf("creating state directory: %w", err)
	}
	lock, err := openStoreLock(ctx, s.path+".lock", s.onLockContention)
	if err != nil {
		return ResetResult{}, err
	}
	old, err := readSnapshotFile(s.path)
	// Reset also recovers oversized/corrupt stores; their cleared count is
	// unknown, just as it is for malformed JSON, but reading remains bounded.
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrStoreTooLarge) {
		return ResetResult{}, closeStoreLock(lock, fmt.Errorf("reading store: %w", err))
	}
	result.Cleared = storedSessionCount(old)
	snap := newSnapshot()
	snap.UpdatedAt = now
	result.Remaining = 0
	if err := ctx.Err(); err != nil {
		return ResetResult{}, closeStoreLock(lock, fmt.Errorf("resetting store: %w", err))
	}
	if err := closeStoreLock(lock, writeSnapshotAtomic(s.path, snap)); err != nil {
		return ResetResult{}, err
	}
	return result, nil
}

func (s *FileStore) setNowForTest(now func() time.Time)   { s.now = now }
func (s *FileStore) setOnLockContentionForTest(fn func()) { s.onLockContention = fn }

func storedSessionCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0
	}
	raw, ok := envelope["sessions"]
	if !ok {
		return 0
	}
	var sessions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return 0
	}

	return len(sessions)
}

func (s *FileStore) withSnapshot(ctx context.Context, mutator func(*snapshot) error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	lock, err := openStoreLock(ctx, s.path+".lock", s.onLockContention)
	if err != nil {
		return err
	}
	snap, err := s.load()
	if err != nil {
		return closeStoreLock(lock, err)
	}
	if err := mutator(&snap); err != nil {
		return closeStoreLock(lock, err)
	}
	if err := validateSnapshot(snap); err != nil {
		return closeStoreLock(lock, fmt.Errorf("validating updated store: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return closeStoreLock(lock, fmt.Errorf("updating store: %w", err))
	}
	return closeStoreLock(lock, writeSnapshotAtomic(s.path, snap))
}

func closeStoreLock(lock *storeLock, err error) error {
	if closeErr := lock.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return err
}

func (s *FileStore) load() (snapshot, error) {
	data, err := readSnapshotFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newSnapshot(), nil
		}
		return snapshot{}, fmt.Errorf("reading store: %w", err)
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return snapshot{}, fmt.Errorf("parsing store %s: %w", s.path, err)
	}
	if snap.SchemaVersion != storeSchemaVersion {
		version := snap.SchemaVersion
		if version == 0 && snap.LegacyVersion != nil {
			version = *snap.LegacyVersion
		}
		return snapshot{}, &UnsupportedSchemaError{Path: s.path, Version: version}
	}
	if snap.Sessions == nil {
		snap.Sessions = make(map[string]Session)
	}
	repairStaleNativeRevivals(&snap)
	if err := validateSnapshot(snap); err != nil {
		return snapshot{}, err
	}
	return snap, nil
}

func readSnapshotFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	// Closing a read-only snapshot has no pending writes to finalize.
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating store: %w", err)
	}
	if info.Size() > maxSnapshotBytes {
		return nil, ErrStoreTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading store: %w", err)
	}
	if len(data) > maxSnapshotBytes {
		return nil, ErrStoreTooLarge
	}
	return data, nil
}

// repairStaleNativeRevivals normalizes snapshots written by versions that
// allowed queued native presence evidence older than process-gone evidence to
// revive the aggregate presence without restoring activity.
//
//nolint:cyclop // every field is required to identify this legacy corruption without masking unrelated damage
func repairStaleNativeRevivals(snap *snapshot) {
	for id, session := range snap.Sessions {
		if session.Activity != nil || session.Presence == PresenceGone ||
			session.Process == nil || session.Observations.Native == nil || session.Observations.Process == nil ||
			session.ActivityDecision == nil {
			continue
		}
		native := session.Observations.Native
		process := session.Observations.Process
		decision := session.ActivityDecision
		if native.Presence == nil || *native.Presence == PresenceGone ||
			process.Present || !session.Process.Equal(process.Process) ||
			decision.Authority != "process" || decision.Reason != "process_gone" || !decision.Process.Equal(process.Process) ||
			native.ObservedAt.After(process.ObservedAt) ||
			!session.PresenceChangedAt.Equal(native.ObservedAt) ||
			!session.ActivityChangedAt.Equal(process.ObservedAt) ||
			!decision.ObservedAt.Equal(process.ObservedAt) {
			continue
		}
		session.Presence = PresenceGone
		session.PresenceChangedAt = process.ObservedAt
		snap.Sessions[id] = session
	}
}

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
	case !session.Harness.IsValid():
		return "invalid harness"
	case session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() || session.UpdatedAt.Before(session.CreatedAt):
		return "invalid session timestamps"
	default:
		return ""
	}
}

func storedSessionStateCorruption(session Session) string {
	if !session.Presence.IsValid() {
		return "invalid presence"
	}
	if reason := storedSessionActivityCorruption(session); reason != "" {
		return reason
	}
	switch {
	case session.Process != nil && !validStoredProcess(*session.Process, false):
		return "invalid process identity"
	case session.Tmux.PanePID < 0:
		return "invalid tmux pane pid"
	case session.Multiplexer.PanePID < 0:
		return "invalid multiplexer pane pid"
	case !session.Multiplexer.Empty() && !session.Multiplexer.Kind.IsValid():
		return "invalid multiplexer kind"
	case session.ActivityDecision != nil && !validStoredActivityDecision(*session.ActivityDecision):
		return "invalid activity decision"
	default:
		return ""
	}
}

func storedSessionActivityCorruption(session Session) string {
	switch {
	case session.Activity == nil && session.Presence != PresenceGone:
		return "non-gone session has null activity"
	case session.Activity != nil && session.Presence == PresenceGone:
		return "gone session has activity"
	case session.Activity != nil && !session.Activity.IsValid():
		return "invalid activity"
	default:
		return ""
	}
}

//nolint:cyclop // each stored observation slot is validated independently
func storedObservationCorruption(observations Observations) string {
	if native := observations.Native; native != nil && !validStoredNativeObservation(*native) {
		return "invalid native observation"
	}
	if process := observations.Process; process != nil && !validStoredProcessObservation(*process) {
		return "invalid process observation"
	}
	if tmux := observations.Tmux; tmux != nil && !validStoredTmuxObservation(*tmux) {
		return "invalid tmux observation"
	}
	if multiplexer := observations.Multiplexer; multiplexer != nil && !validStoredMultiplexerObservation(*multiplexer) {
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

func validStoredTmuxObservation(observation TmuxObservation) bool {
	return !observation.ObservedAt.IsZero() && validStoredProcess(observation.Process, false) && observation.Context.PanePID >= 0
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

func newSnapshot() snapshot {
	return snapshot{SchemaVersion: storeSchemaVersion, LegacyVersion: nil, UpdatedAt: time.Time{}, Sessions: make(map[string]Session)}
}

func writeSnapshotAtomic(path string, snap snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding store: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxSnapshotBytes {
		return ErrStoreTooLarge
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp store: %w", err)
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		if keep {
			return
		}
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("setting temp store permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("writing temp store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("syncing temp store: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing temp store: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("renaming temp store: %w", err)
	}
	keep = true
	return syncDir(dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening state directory: %w", err)
	}
	defer func() { _ = handle.Close() }()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("syncing state directory: %w", err)
	}
	return nil
}
