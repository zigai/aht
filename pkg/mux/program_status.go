package mux

import (
	"context"
	"errors"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	ProgramStateIdle    ProgramState = "idle"
	ProgramStateWorking ProgramState = "working"
	ProgramStateDone    ProgramState = "done"
	ProgramStateBlocked ProgramState = "blocked"
	ProgramStateError   ProgramState = "error"
)

const (
	programKindPermission = "permission"
	programKindQuestion   = "question"
)

// ErrProgramStatusUnsignaled reports a server that cannot signal program
// status changes, so its panes have to be polled.
var ErrProgramStatusUnsignaled = errors.New("multiplexer does not signal program status changes")

// ProgramStatusWatcher is an optional capability interface for multiplexers
// that signal a program status change.
type ProgramStatusWatcher interface {
	// WatchProgramStatus calls changed with the server's panes once the watch
	// is set, and again after the state or kind of a pane's own record changes.
	// It returns nil when ctx ends, [ErrProgramStatusUnsignaled] when the
	// server cannot signal changes, and another error when the server stops
	// answering.
	WatchProgramStatus(ctx context.Context, serverIdentity string, changed func([]Pane)) error
}

// ProgramStatusWatch watches one multiplexer server as
// [ProgramStatusWatcher.WatchProgramStatus] does.
type ProgramStatusWatch func(
	ctx context.Context,
	kind registry.MultiplexerKind,
	serverIdentity string,
	changed func([]Pane),
) error

// ProgramState is a state of the OSC 7501 program status protocol.
type ProgramState string

// ProgramStatus is the record a pane's program reported for itself with the
// OSC 7501 program status protocol. Records with an ID are not included.
type ProgramStatus struct {
	// App is the reporting program's stable name, such as "claude-code".
	App   string
	State ProgramState
	// Kind says what a blocked program waits for: permission, question or auth.
	Kind string
}

// Activity maps the record to an activity and its detail. It reports false
// for a state the protocol does not define.
func (s ProgramStatus) Activity() (registry.Activity, registry.ActivityDetail, bool) {
	switch s.State {
	case ProgramStateWorking:
		return registry.ActivityRunning, "", true
	case ProgramStateBlocked:
		return registry.ActivityWaiting, s.waitingDetail(), true
	case ProgramStateIdle, ProgramStateDone:
		return registry.ActivityIdle, "", true
	case ProgramStateError:
		return registry.ActivityFailed, registry.ActivityDetailGeneral, true
	}

	return "", "", false
}

func (s ProgramStatus) waitingDetail() registry.ActivityDetail {
	switch s.Kind {
	case programKindPermission:
		return registry.ActivityDetailPermission
	case programKindQuestion:
		return registry.ActivityDetailQuestion
	}

	return ""
}
