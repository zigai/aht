package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	harnesspkg "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
)

const (
	stopTargetMaxAge         = 30 * time.Minute
	stopSummaryMaxRuleWidth  = 100
	stopSummaryFallbackWidth = 80
)

var (
	errManageStopAllFailed = errors.New("one or more sessions failed to stop")
	errStateResetForce     = errors.New("--force is required to reset stored session state")
	errStopTargetSkipped   = errors.New("session was not stopped")
	errUnknownStopMethod   = errors.New("unknown stop method")
	errTerminalRequired    = errors.New("confirmation requires a terminal")
)

type manageResetResult struct {
	registry.ResetResult

	Path string `json:"path"`
}
type manageStopAllOptions struct {
	dryRun   bool
	signaler sessionStopSignaler
}
type sessionStopSignaler interface {
	ValidateStopTarget(ctx context.Context, session registry.Session, target stopTarget) (stopTargetValidation, error)
	SendTmuxInterrupt(ctx context.Context, serverIdentity, paneID string) error
	SendProcessInterrupt(pid int) error
}
type (
	defaultSessionStopSignaler struct{}
	stopTargetValidation       struct {
		OK     bool
		Reason string
	}
)

type stopTarget struct {
	Method, Target, ServerIdentity string
	PID                            int
}
type manageStopAllResult struct {
	Stoppable int                       `json:"stoppable"`
	Stopped   int                       `json:"stopped"`
	Skipped   int                       `json:"skipped"`
	Failed    int                       `json:"failed"`
	DryRun    bool                      `json:"dry_run,omitempty"`
	Results   []manageStopSessionResult `json:"results,omitempty"`
}
type manageStopSessionResult struct {
	ID       string             `json:"id"`
	Harness  registry.Harness   `json:"harness"`
	Presence registry.Presence  `json:"presence"`
	Activity *registry.Activity `json:"activity"`
	Method   string             `json:"method,omitempty"`
	Target   string             `json:"target,omitempty"`
	Status   string             `json:"status"`
	Reason   string             `json:"reason,omitempty"`
	Error    string             `json:"error,omitempty"`
}

func (defaultSessionStopSignaler) ValidateStopTarget(ctx context.Context, s registry.Session, t stopTarget) (stopTargetValidation, error) {
	latest := time.Time{}
	if s.Observations.Process != nil {
		latest = s.Observations.Process.ObservedAt
	}
	if s.Observations.Tmux != nil && s.Observations.Tmux.ObservedAt.After(latest) {
		latest = s.Observations.Tmux.ObservedAt
	}
	if latest.IsZero() || time.Since(latest) > stopTargetMaxAge {
		return stopTargetValidation{Reason: "observation too old"}, nil
	}
	switch t.Method {
	case "tmux-interrupt":
		return validateTmuxStopTarget(ctx, s)
	case "pid-interrupt":
		return validateProcessStopTarget(ctx, s)
	default:
		return stopTargetValidation{}, fmt.Errorf("%w: %q", errUnknownStopMethod, t.Method)
	}
}

func (defaultSessionStopSignaler) SendTmuxInterrupt(ctx context.Context, serverIdentity, paneID string) error {
	if err := tmux.SendInterruptTo(ctx, serverIdentity, paneID); err != nil {
		return fmt.Errorf("send tmux interrupt: %w", err)
	}
	return nil
}

func (defaultSessionStopSignaler) SendProcessInterrupt(pid int) error {
	p, e := os.FindProcess(pid)
	if e != nil {
		return fmt.Errorf("find process %d: %w", pid, e)
	}
	if err := p.Signal(os.Interrupt); err != nil {
		return fmt.Errorf("signal process %d: %w", pid, err)
	}
	return nil
}

func (app *application) newRegistryResetCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:           "reset",
		Short:         "Reset stored session state",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return exitCode(errStateResetForce, exitCodeUsage)
			}
			s := app.store()
			r, e := s.Reset(cmd.Context())
			if e != nil {
				return fmt.Errorf("resetting store: %w", e)
			}
			o := manageResetResult{ResetResult: r, Path: s.Path()}
			if app.outputJSON {
				return app.writeJSON(o)
			}
			return app.writeHumanDetails([]humanDetail{
				{label: "Cleared", value: strconv.Itoa(o.Cleared)},
				{label: "Remaining", value: strconv.Itoa(o.Remaining)},
				{label: "Path", value: o.Path},
			})
		},
	}
	command.Flags().BoolVarP(&force, "force", "f", false, "confirm destructive state reset")
	return command
}

func (app *application) resolveStopSessions(ctx context.Context, args []string, all bool) ([]registry.Session, error) {
	if all {
		listed, err := app.store().List(ctx, registry.Filter{Presence: registry.PresenceLive})
		if err != nil {
			return nil, fmt.Errorf("list live sessions: %w", err)
		}
		return listed, nil
	}
	seen := make(map[string]bool, len(args))
	sessions := make([]registry.Session, 0, len(args))
	for _, arg := range args {
		session, err := app.resolveSession(ctx, arg)
		if err != nil {
			return nil, err
		}
		if seen[session.ID] {
			continue
		}
		seen[session.ID] = true
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (app *application) runStop(ctx context.Context, args []string, all bool, dryRun bool) error {
	sessions, err := app.resolveStopSessions(ctx, args, all)
	if err != nil {
		return err
	}
	result, err := runManageStopSessions(ctx, sessions, manageStopAllOptions{dryRun: dryRun})
	if writeErr := app.writeManageStopAllResult(result); writeErr != nil {
		return writeErr
	}
	if err == nil && !all && result.Stoppable == 0 && result.Stopped == 0 && result.Skipped > 0 {
		reason := "no safe stop target"
		if len(result.Results) > 0 && result.Results[0].Reason != "" {
			reason = result.Results[0].Reason
		}
		return fmt.Errorf("%w: %s", errStopTargetSkipped, reason)
	}
	return err
}

func (app *application) confirmStopAll(ctx context.Context, in io.Reader) (bool, error) {
	return app.confirmAction(ctx, in, "Stop all live sessions? [y/N]: ", "--yes")
}

func (app *application) confirmAction(ctx context.Context, in io.Reader, prompt string, correctiveFlag string) (bool, error) {
	if in == nil {
		in = app.stdin
	}
	if in == nil {
		in = os.Stdin
	}
	file, isFile := in.(*os.File)
	if !isFile || !term.IsTerminal(int(file.Fd())) {
		return false, exitCode(fmt.Errorf("%w; use %s to confirm non-interactively", errTerminalRequired, correctiveFlag), exitCodeUsage)
	}
	out := app.stderr
	if out == nil {
		out = os.Stderr
	}
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, fmt.Errorf("writing confirmation prompt: %w", err)
	}
	return readConfirmationLine(ctx, file)
}

func readConfirmationLine(ctx context.Context, file *os.File) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, exitCode(err, exitCodeInterrupted)
	}
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		return false, fmt.Errorf("pipe for confirmation: %w", err)
	}
	defer func() {
		_ = rPipe.Close()
		_ = wPipe.Close()
	}()

	stop := context.AfterFunc(ctx, func() {
		_, _ = wPipe.Write([]byte{1})
	})
	defer stop()

	pollFds := []unix.PollFd{
		{Fd: int32(file.Fd()), Events: unix.POLLIN},  //nolint:gosec // terminal file descriptor fits in int32
		{Fd: int32(rPipe.Fd()), Events: unix.POLLIN}, //nolint:gosec // pipe file descriptor fits in int32
	}
	for {
		_, err := unix.Poll(pollFds, -1)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return false, fmt.Errorf("polling confirmation: %w", err)
		}
		break
	}
	if pollFds[1].Revents&unix.POLLIN != 0 || ctx.Err() != nil {
		return false, exitCode(context.Canceled, exitCodeInterrupted)
	}

	reader := bufio.NewReader(file)
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}

func runManageStopSessions(ctx context.Context, ss []registry.Session, o manageStopAllOptions) (manageStopAllResult, error) {
	if o.signaler == nil {
		o.signaler = defaultSessionStopSignaler{}
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
	r := manageStopAllResult{DryRun: o.dryRun, Results: make([]manageStopSessionResult, 0, len(ss))}
	seen := map[string]bool{}
	for _, s := range ss {
		entry := manageStopSessionResult{ID: s.ID, Harness: s.Harness, Presence: s.Presence, Activity: s.Activity, Status: "skipped"}
		if s.Presence != registry.PresenceLive {
			entry.Reason = "session is not live"
			r.Skipped++
			r.Results = append(r.Results, entry)
			continue
		}
		t, ok := stopTargetForSession(s)
		if !ok {
			entry.Reason = "no stop target"
			r.Skipped++
			r.Results = append(r.Results, entry)
			continue
		}
		entry.Method = t.Method
		entry.Target = t.Target
		v, e := o.signaler.ValidateStopTarget(ctx, s, t)
		if e != nil {
			entry.Status = "failed"
			entry.Error = e.Error()
			r.Failed++
			r.Results = append(r.Results, entry)
			continue
		}
		if !v.OK {
			entry.Reason = v.Reason
			r.Skipped++
			r.Results = append(r.Results, entry)
			continue
		}
		k := t.Method + "\x00" + t.ServerIdentity + "\x00" + t.Target
		if seen[k] {
			entry.Reason = "duplicate target"
			r.Skipped++
			r.Results = append(r.Results, entry)
			continue
		}
		seen[k] = true
		r.Stoppable++
		if o.dryRun {
			entry.Status = "would_stop"
			r.Results = append(r.Results, entry)
			continue
		}
		if e = sendStopSignal(ctx, o.signaler, t); e != nil {
			entry.Status = "failed"
			entry.Error = e.Error()
			r.Failed++
			r.Results = append(r.Results, entry)
			continue
		}
		entry.Status = "stopped"
		r.Stopped++
		r.Results = append(r.Results, entry)
	}
	if r.Failed > 0 {
		return r, errManageStopAllFailed
	}
	return r, nil
}

func validateTmuxStopTarget(ctx context.Context, s registry.Session) (stopTargetValidation, error) {
	panes, e := tmux.ListPanes(ctx)
	if e != nil {
		return stopTargetValidation{}, fmt.Errorf("list tmux panes: %w", e)
	}
	return tmuxStopTargetValidation(s, panes), nil
}

func tmuxStopTargetValidation(session registry.Session, panes []tmux.Pane) stopTargetValidation {
	paneIDFound := false
	for _, p := range panes {
		if p.Tmux.PaneID != session.Tmux.PaneID {
			continue
		}
		paneIDFound = true
		if tmuxTargetMatchesSession(session.Tmux, p.Tmux) {
			return stopTargetValidation{OK: true}
		}
	}
	if paneIDFound {
		return stopTargetValidation{Reason: "tmux pane identity changed"}
	}
	return stopTargetValidation{Reason: "tmux pane no longer exists"}
}

func validateProcessStopTarget(ctx context.Context, s registry.Session) (stopTargetValidation, error) {
	if s.Process == nil || !s.Process.Complete() {
		return stopTargetValidation{Reason: "missing process start identity"}, nil
	}
	id := processinfo.StartIdentity(ctx, s.Process.PID)
	if id == "" {
		return stopTargetValidation{Reason: "process no longer exists"}, nil
	}
	if id != s.Process.StartIdentity {
		return stopTargetValidation{Reason: "process identity changed"}, nil
	}
	cmd, e := processinfo.CommandName(ctx, s.Process.PID)
	if e != nil {
		return stopTargetValidation{}, fmt.Errorf("read process command: %w", e)
	}
	if !harnessCommandMatches(s.Harness, cmd) {
		return stopTargetValidation{Reason: "process command changed"}, nil
	}
	return stopTargetValidation{OK: true}, nil
}

func tmuxTargetMatchesSession(a, b registry.TmuxContext) bool {
	if a.ServerSocket == "" || b.ServerSocket == "" || a.ServerSocket != b.ServerSocket {
		return false
	}
	if a.PanePID > 0 && b.PanePID > 0 && a.PanePID != b.PanePID {
		return false
	}
	for _, p := range [][2]string{{a.SessionID, b.SessionID}, {a.SessionName, b.SessionName}, {a.WindowID, b.WindowID}, {a.WindowIndex, b.WindowIndex}, {a.PaneIndex, b.PaneIndex}} {
		if p[0] != "" && p[1] != "" && p[0] != p[1] {
			return false
		}
	}
	return true
}

func harnessCommandMatches(h registry.Harness, c string) bool {
	base := filepath.Base(strings.TrimSpace(c))
	return slices.Contains(harnesspkg.ProcessNames(h), base)
}

func stopTargetForSession(s registry.Session) (stopTarget, bool) {
	if s.Tmux.PaneID != "" && s.Tmux.ServerSocket != "" {
		return stopTarget{Method: "tmux-interrupt", Target: s.Tmux.PaneID, ServerIdentity: s.Tmux.ServerSocket}, true
	}
	if s.Process != nil && s.Process.PID > 0 {
		return stopTarget{Method: "pid-interrupt", Target: strconv.Itoa(s.Process.PID), PID: s.Process.PID}, true
	}
	return stopTarget{}, false
}

func sendStopSignal(ctx context.Context, s sessionStopSignaler, t stopTarget) error {
	switch t.Method {
	case "tmux-interrupt":
		if err := s.SendTmuxInterrupt(ctx, t.ServerIdentity, t.Target); err != nil {
			return fmt.Errorf("send tmux interrupt: %w", err)
		}
		return nil
	case "pid-interrupt":
		if err := s.SendProcessInterrupt(t.PID); err != nil {
			return fmt.Errorf("send process interrupt: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", errUnknownStopMethod, t.Method)
	}
}

func (app *application) writeManageStopAllResult(r manageStopAllResult) error {
	const (
		stopIDWidth       = 16
		stopAgentWidth    = 10
		stopPresenceWidth = 8
		stopActivityWidth = 8
		stopStatusWidth   = 10
		stopMethodWidth   = 14
		stopTargetWidth   = 12
		stopDetailWidth   = 28
	)
	if app.outputJSON {
		return app.writeJSON(r)
	}
	rows := make([][]string, 0, len(r.Results))
	idWidth, targetWidth := stopIDWidth, stopTargetWidth
	for _, result := range r.Results {
		idWidth = max(idWidth, text.StringWidth(result.ID))
		targetWidth = max(targetWidth, text.StringWidth(result.Target))
		detail := result.Reason
		if result.Error != "" {
			detail = result.Error
		}
		rows = append(rows, []string{result.ID, string(result.Harness), string(result.Presence), activityString(result.Activity), result.Status, result.Method, result.Target, detail})
	}
	if err := app.writeWrappedHumanTable(
		[]humanColumn{{heading: "ID", width: idWidth, wrap: wrapHumanIdentifier}, {heading: "Agent", width: stopAgentWidth}, {heading: "Presence", width: stopPresenceWidth}, {heading: "Activity", width: stopActivityWidth}, {heading: "Status", width: stopStatusWidth}, {heading: "Method", width: stopMethodWidth}, {heading: "Target", width: targetWidth, wrap: wrapHumanIdentifier}, {heading: "Detail", width: stopDetailWidth}},
		rows,
	); err != nil {
		return err
	}
	ruleWidth := min(app.maxLineWidth(), stopSummaryMaxRuleWidth)
	if ruleWidth <= 0 {
		ruleWidth = stopSummaryFallbackWidth
	}
	if err := app.writeln(strings.Repeat("─", ruleWidth)); err != nil {
		return err
	}
	return app.writef("Summary: stoppable=%d stopped=%d skipped=%d failed=%d\n", r.Stoppable, r.Stopped, r.Skipped, r.Failed)
}
