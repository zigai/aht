package reporting

import "errors"

const reportProcessAncestorLimit = 16

var (
	errInvalidAttribute       = errors.New("invalid attribute")
	errMissingReportHarness   = errors.New("missing harness")
	errConflictingReportStdin = errors.New(
		"--raw-stdin and --raw-stdin-defaults-only cannot be used together",
	)
	errMissingReportIdentity   = errors.New("missing report identity or transition")
	errGonePresenceActivity    = errors.New("gone presence cannot include activity")
	errProcessEvidenceSequence = errors.New("process evidence cannot include sequence")
	errProcessEvidenceIdentity = errors.New("process evidence requires pid and start identity")
	errProcessEvidenceActivity = errors.New("process evidence cannot include activity")
)

type InputError struct{ err error }

func (e *InputError) Error() string { return e.err.Error() }
func (e *InputError) Unwrap() error { return e.err }
func invalidInput(err error) error  { return &InputError{err: err} }
