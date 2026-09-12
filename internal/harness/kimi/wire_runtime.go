package kimi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sethvargo/go-retry"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

const (
	frameLimit          = 8 << 20
	operationTimeout    = 5 * time.Second
	shutdownTimeout     = 3 * time.Second
	sigintExitCode      = 130
	sigpipeExitCode     = 141
	sessionPollInterval = 25 * time.Millisecond
)

var (
	errIdentity               = errors.New("kimi wire requires current native hook identity for its owned process; run aht manage integrations install kimi-code, then retry with the same AHT store")
	errCompetingModeOptions   = errors.New("AHT selects native --wire; remove competing native mode options")
	errMissingOptionValue     = errors.New("native Kimi option is missing its value")
	errSubcommandUnsupported  = errors.New("wire accepts native Kimi options, not native subcommands; use --option=value for extension options")
	errWireOSStreamsRequired  = errors.New("kimi wire requires OS-backed stdin, stdout and stderr")
	errWireInputPlatform      = errors.New("cannot open cancellable Wire input; Linux or macOS is required")
	errWireOutputPlatform     = errors.New("cannot open cancellable Wire output; Linux or macOS is required")
	errWireInputPipe          = errors.New("cannot create native Wire input pipe")
	errWireOutputPipe         = errors.New("cannot create native Wire output pipe")
	errStartNativeKimi        = errors.New("cannot start native kimi; install Kimi Code and ensure kimi is on PATH")
	errWireProtocolInvalid    = errors.New("native Kimi Wire protocol is invalid or exceeds supported bounds")
	errWireShutdownTimeout    = errors.New("native Kimi Wire did not close within the shutdown deadline")
	errReapNativeKimi         = errors.New("cannot reap native Kimi process")
	errFrameLimitExceededLong = errors.New("wire message exceeds the 8 MiB frame limit")
	errForwardWireMessage     = errors.New("cannot forward Wire message; check the connected client")
	errReadWireStream         = errors.New("cannot read Wire stream")
	errPublishActivity        = errors.New("cannot publish Kimi Wire activity to AHT; check the selected store and tracker")
	errReadSessionEvidence    = errors.New("cannot read AHT native session evidence; check the selected store and tracker")
)

// Options identifies the native invocation and its external JSONL streams.
// Run duplicates the input and output descriptors and never closes the originals.
// Stderr is inherited directly by the child, without capturing native output.
type Options struct {
	Args      []string
	StorePath string
	Stdin     *os.File
	Stdout    *os.File
	Stderr    *os.File
}

// ExitError preserves a native exit or a transport signal exit without host data.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("kimi wire exited with status %d", e.Code) }
func (e *ExitError) ExitCode() int { return e.Code }

// ValidateArgs rejects mode switches before any process or registry side effects.
func ValidateArgs(args []string) error {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, _, inline := strings.Cut(arg, "=")
		switch name {
		case "--wire", "--acp", "--print", "--quiet", "--ui", "--input-format", "--output-format", "--final-message-only", "--shell", "--web", "--help", "-h", "--version", "-V":
			return errCompetingModeOptions
		case "--work-dir", "-w", "--add-dir", "--session", "--resume", "-S", "-r", "--config", "--config-file", "--model", "-m", "--prompt", "-p", "--command", "-c", "--agent", "--agent-file", "--mcp-config-file", "--mcp-config", "--skills-dir", "--max-steps-per-turn", "--max-retries-per-step", "--max-ralph-iterations":
			if !inline {
				index++
				if index == len(args) {
					return errMissingOptionValue
				}
			}
		default:
			if arg == "--" || !strings.HasPrefix(arg, "-") {
				return errSubcommandUnsupported
			}
		}
	}
	return nil
}

// Run forwards native Wire messages while synchronously publishing authoritative
// transitions. Only native hook evidence for this child can establish identity.
//
//nolint:gocognit,cyclop // Run manages bidirectional frame pumps, timeout bounds, and lifecycle processes.
func Run(ctx context.Context, options Options) error {
	if err := ValidateArgs(options.Args); err != nil {
		return err
	}
	if options.Stdin == nil || options.Stdout == nil || options.Stderr == nil {
		return errWireOSStreamsRequired
	}
	if ctx.Err() != nil {
		return &ExitError{Code: sigintExitCode}
	}
	input, restoreInput, err := duplicateStream(options.Stdin)
	if err != nil {
		return errWireInputPlatform
	}
	defer restoreInput()
	output, restoreOutput, err := duplicateStream(options.Stdout)
	if err != nil {
		return errWireOutputPlatform
	}
	defer restoreOutput()
	childIn, toChild, err := os.Pipe()
	if err != nil {
		return errWireInputPipe
	}
	defer func() { _ = childIn.Close() }()
	defer func() { _ = toChild.Close() }()
	fromChild, childOut, err := os.Pipe()
	if err != nil {
		return errWireOutputPipe
	}
	defer func() { _ = fromChild.Close() }()
	defer func() { _ = childOut.Close() }()

	command := exec.CommandContext(ctx, "kimi", append([]string{"--wire"}, options.Args...)...)
	command.Stdin, command.Stdout, command.Stderr = childIn, childOut, options.Stderr
	command.Env = append(os.Environ(), registry.StorePathEnv+"="+options.StorePath)
	if err := ownProcessGroup(command); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return errStartNativeKimi
	}
	_ = childIn.Close()
	_ = childOut.Close()
	//nolint:exhaustruct_v5 // remaining process fields are discovered through native hooks
	identity := registry.ProcessIdentity{PID: command.Process.Pid, StartIdentity: processinfo.StartIdentity(ctx, command.Process.Pid)}
	childDone := make(chan error, 1)
	go func() { childDone <- command.Wait() }()
	transportCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopIO := func() {
		cancel()
		killProcessGroup(command)
		_ = input.Close()
		_ = output.Close()
		_ = toChild.Close()
		_ = fromChild.Close()
	}
	if !identity.Complete() {
		stopIO()
		<-childDone
		return errIdentity
	}
	var protocol Protocol
	//nolint:exhaustruct_v5 // default socket and routing mode are selected
	sink := client.New(client.Config{StorePath: options.StorePath})
	inputDone := make(chan error, 1)
	hostDone := make(chan error, 1)
	go func() {
		inputDone <- pumpFrames(input, toChild, func(line []byte) error { protocol.ObserveClient(line); return nil })
		_ = toChild.Close()
	}()
	go func() {
		hostDone <- pumpFrames(fromChild, output, func(line []byte) error {
			update, changed, observeErr := protocol.ObserveHost(line)
			if observeErr != nil {
				return errWireProtocolInvalid
			}
			if !changed {
				return nil
			}
			return publish(transportCtx, sink, identity, update)
		})
	}()

	var result, childErr error
	var inputFinished, hostFinished, childFinished bool
	var timeout <-chan time.Time
	timer := time.NewTimer(shutdownTimeout)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	startShutdown := func() {
		if timeout == nil {
			timer.Reset(shutdownTimeout)
			timeout = timer.C
		}
	}
	for !hostFinished || !childFinished {
		select {
		case <-ctx.Done():
			result = &ExitError{Code: sigintExitCode}
		case err := <-inputDone:
			inputFinished = true
			inputDone = nil
			result = err
			startShutdown()
		case err := <-hostDone:
			hostFinished = true
			hostDone = nil
			result = err
			startShutdown()
		case childErr = <-childDone:
			childFinished = true
			childDone = nil
			startShutdown()
		case <-timeout:
			result = errWireShutdownTimeout
		}
		if result != nil {
			break
		}
	}
	stopIO()
	if !inputFinished {
		<-inputDone
	}
	if !hostFinished {
		<-hostDone
	}
	if !childFinished {
		childErr = <-childDone
	}
	if result != nil {
		return result
	}
	if childErr != nil {
		if exit, ok := errors.AsType[*exec.ExitError](childErr); ok {
			return &ExitError{Code: nativeExitCode(exit)}
		}
		return errReapNativeKimi
	}
	return nil
}

//nolint:gocognit // pumpFrames handles line framing, size limits, and write deadlines
func pumpFrames(input io.Reader, output *os.File, observe func([]byte) error) error {
	reader := bufio.NewReader(input)
	var frame []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(frame)+len(part) > frameLimit {
			return errFrameLimitExceededLong
		}
		frame = append(frame, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(frame) > 0 {
			if observeErr := observe(frame); observeErr != nil {
				return observeErr
			}
			// Deadlines bound a stalled external client or native reader. Regular
			// files do not support deadlines, but their writes do not await peers.
			_ = output.SetWriteDeadline(time.Now().Add(operationTimeout))
			if _, writeErr := output.Write(frame); writeErr != nil {
				if isBrokenPipe(writeErr) {
					return &ExitError{Code: sigpipeExitCode}
				}
				return errForwardWireMessage
			}
			frame = frame[:0]
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errReadWireStream
		}
	}
}

func publish(ctx context.Context, sink *client.Client, process registry.ProcessIdentity, update Update) error {
	operationCtx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	session, err := nativeSession(operationCtx, sink, process)
	if err != nil {
		return err
	}
	attributes := map[string]string{
		"aht_integration":         "kimi-wire",
		"aht_integration_version": strconv.Itoa(harness.IntegrationVersion),
		"aht_interaction_mode":    "wire",
	}
	//nolint:exhaustruct_v5 // native Wire observes activity transitions on existing native sessions
	_, err = sink.Observe(operationCtx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness:  registry.HarnessKimiCode,
		Identity: registry.ObservationIdentity{SessionID: session.SessionID, SessionPath: session.SessionPath},
		Activity: &update.Activity, NativeEvent: update.Event, Process: session.Process,
		Tmux: &session.Tmux, Multiplexer: &session.Multiplexer,
		Catalog:    &registry.CatalogMetadata{ResumeCommand: session.ResumeCommand, CWD: session.CWD, ProjectRoot: session.ProjectRoot},
		Attributes: attributes, ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		return errPublishActivity
	}
	return nil
}

//nolint:cyclop // nativeSession waits for active native identity matching the child process
func nativeSession(ctx context.Context, sink *client.Client, process registry.ProcessIdentity) (registry.Session, error) {
	var found registry.Session

	err := retry.Do(ctx, retry.NewConstant(sessionPollInterval), func(ctx context.Context) error {
		//nolint:exhaustruct_v5 // session lookup filters by harness alone
		sessions, err := sink.List(ctx, registry.Filter{Harness: registry.HarnessKimiCode})
		if err != nil {
			return errReadSessionEvidence
		}

		for _, session := range sessions {
			native := session.Observations.Native
			if native == nil || !native.Process.Equal(process) || session.Process == nil || !session.Process.Equal(process) || session.SessionID == "" {
				continue
			}
			if native.Attributes["aht_integration_version"] != strconv.Itoa(harness.IntegrationVersion) {
				continue
			}
			source := native.Attributes["aht_integration"]
			if source == "kimi-code-hook" || source == "kimi-wire" {
				found = session

				return nil
			}
		}

		return retry.RetryableError(errIdentity)
	})
	if err != nil {
		if context.Cause(ctx) != nil {
			return registry.Session{}, errIdentity
		}

		return registry.Session{}, fmt.Errorf("polling native Kimi session: %w", err)
	}

	return found, nil
}
