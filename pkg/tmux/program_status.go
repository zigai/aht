package tmux

import (
	"context"
	"errors"
	"fmt"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/pkg/mux"
)

const (
	programStatusHook      = "pane-program-status-changed"
	programStatusHookIndex = 7501
	programStatusChannel   = "aht-program-status"
	programStatusWait      = 5 * time.Minute

	// The hook fires for every report, including a new message or a child
	// record. Comparing with the pane option signals a state or kind change only.
	programStatusChanged = "#{!=:#{program_state}#{program_kind},#{@aht-program-status}}"
	programStatusSignal  = `set-option -pF @aht-program-status "#{program_state}#{program_kind}" ; ` +
		"wait-for -S " + programStatusChannel
)

// WatchProgramStatus calls changed with the server's panes once the watch is
// set, and again after the state or kind of a pane's own program status record
// changes. It sets a global hook that signals a wait-for channel; the hook
// stays on the server after ctx ends. It returns
// [mux.ErrProgramStatusUnsignaled] when the server's tmux has no such hook.
func WatchProgramStatus(
	ctx context.Context,
	serverIdentity string,
	changed func([]mux.Pane),
) error {
	cfg, err := gotmuxConfigForIdentity(serverIdentity)
	if err != nil {
		return err
	}

	server, err := gotmux.New(cfg)
	if err != nil {
		return fmt.Errorf("init tmux client: %w", err)
	}

	hook, err := programStatusHookCommands()
	if err != nil {
		return err
	}

	for ctx.Err() == nil {
		// Setting the hook again after each wait restores one the user's
		// configuration removed.
		err := server.GlobalHooks().Set(ctx, programStatusHook, programStatusHookIndex, hook)
		if err != nil {
			err = programStatusHookError(err)
		} else {
			err = awaitProgramStatus(ctx, server, serverIdentity, changed)
		}

		if err != nil && ctx.Err() == nil {
			return err
		}
	}

	return nil
}

func programStatusHookCommands() (gotmux.CommandSequence, error) {
	var empty gotmux.CommandSequence

	signal, err := gotmux.NewCommand("if-shell", "-F", programStatusChanged, programStatusSignal)
	if err != nil {
		return empty, fmt.Errorf("build tmux program status hook: %w", err)
	}

	hook, err := gotmux.Sequence(signal)
	if err != nil {
		return empty, fmt.Errorf("build tmux program status hook: %w", err)
	}

	return hook, nil
}

func awaitProgramStatus(
	ctx context.Context,
	server *gotmux.Server,
	serverIdentity string,
	changed func([]mux.Pane),
) error {
	waitCtx, cancel := context.WithTimeout(ctx, programStatusWait)
	defer cancel()

	for {
		panes, err := serverPanes(waitCtx, serverIdentity)
		if err == nil {
			changed(panes)

			err = server.WaitFor(waitCtx, programStatusChannel)
		}

		if waitCtx.Err() != nil {
			return nil
		}

		if err != nil {
			return fmt.Errorf("wait for tmux program status: %w", err)
		}
	}
}

func serverPanes(ctx context.Context, serverIdentity string) ([]mux.Pane, error) {
	listed, err := queryServerPanesGotmux(ctx, serverSpec{Identity: serverIdentity})
	if err != nil {
		return nil, err
	}

	listed = appendCanonicalPanes(
		nil,
		listed,
		serverIdentity,
		make(map[string]struct{}, len(listed)),
	)

	panes := make([]mux.Pane, 0, len(listed))
	for _, pane := range listed {
		panes = append(panes, pane.ToMuxPane())
	}

	return panes, nil
}

func programStatusHookError(err error) error {
	command, answered := errors.AsType[*gotmux.CommandError](err)
	if answered && command.Timeout == gotmux.TimeoutSourceNone &&
		!errors.Is(err, gotmux.ErrNoServer) {
		return fmt.Errorf("%w: %w", mux.ErrProgramStatusUnsignaled, err)
	}

	return fmt.Errorf("set tmux program status hook: %w", err)
}
