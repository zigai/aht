package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/registry"

	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/tmux"
)

const (
	stopTargetMaxAge         = 30 * time.Minute
	stopSummaryMaxRuleWidth  = 100
	stopSummaryFallbackWidth = 80
	stopMethodProcess        = "pid-interrupt"
)

var (
	errManageStopAllFailed = errors.New("one or more sessions failed to stop")
	errStateResetForce     = errors.New("--force is required to reset stored session state")
	errStopTargetSkipped   = errors.New("session was not stopped")
	errUnknownStopMethod   = errors.New("unknown stop method")
	errNoPaneInterrupt     = errors.New("multiplexer cannot interrupt a pane")
	errTerminalRequired    = errors.New("confirmation requires a terminal")

	paneStoppers = map[registry.MultiplexerKind]paneStopper{
		registry.MultiplexerTmux: {
			method:      "tmux-interrupt",
			validate:    validateTmuxStopTarget,
			interrupter: tmux.NewDriver(),
		},
	}
)

var (
	errStopSelection       = errors.New("provide one or more sessions, or --all")
	errStopAllConfirmation = errors.New(
		"stopping all sessions was not confirmed (pass -y to confirm)",
	)
)

//exhaustruct:optional
type manageStopAllOptions struct {
	dryRun   bool
	signaler sessionStopSignaler
}

type sessionStopSignaler interface {
	ValidateStopTarget(
		ctx context.Context,
		session registry.Session,
		target stopTarget,
	) (stopTargetValidation, error)
	SendMultiplexerInterrupt(
		ctx context.Context,
		kind registry.MultiplexerKind,
		serverIdentity, paneID string,
	) error
	SendProcessInterrupt(pid int) error
}

type (
	defaultSessionStopSignaler struct{}
	stopTargetValidation       struct {
		OK bool
		//exhaustruct:optional
		Reason string
	}
)

type stopTarget struct {
	Method, Target string
	//exhaustruct:optional
	Kind registry.MultiplexerKind
	//exhaustruct:optional
	ServerIdentity string
	//exhaustruct:optional
	PID int
}

type paneStopper struct {
	method      string
	validate    func(context.Context, registry.Session) (stopTargetValidation, error)
	interrupter mux.Interrupter
}

type manageStopAllResult struct {
	//exhaustruct:optional
	Stoppable int `json:"stoppable"`
	//exhaustruct:optional
	Stopped int `json:"stopped"`
	//exhaustruct:optional
	Skipped int `json:"skipped"`
	//exhaustruct:optional
	Failed  int                       `json:"failed"`
	DryRun  bool                      `json:"dry_run,omitempty"`
	Results []manageStopSessionResult `json:"results,omitempty"`
}

type manageStopSessionResult struct {
	ID       string             `json:"id"`
	Harness  registry.Harness   `json:"harness"`
	Presence registry.Presence  `json:"presence"`
	Activity *registry.Activity `json:"activity"`
	//exhaustruct:optional
	Method string `json:"method,omitempty"`
	//exhaustruct:optional
	Target string `json:"target,omitempty"`
	Status string `json:"status"`
	//exhaustruct:optional
	Reason string `json:"reason,omitempty"`
	//exhaustruct:optional
	Error string `json:"error,omitempty"`
}

func (defaultSessionStopSignaler) ValidateStopTarget(
	ctx context.Context,
	s registry.Session,
	t stopTarget,
) (stopTargetValidation, error) {
	latest := time.Time{}
	if s.Observations.Process != nil {
		latest = s.Observations.Process.ObservedAt
	}

	if s.Observations.Location != nil && s.Observations.Location.ObservedAt.After(latest) {
		latest = s.Observations.Location.ObservedAt
	}

	if latest.IsZero() || time.Since(latest) > stopTargetMaxAge {
		return stopTargetValidation{OK: false, Reason: "observation too old"}, nil
	}

	if t.Method == stopMethodProcess {
		if s.Process == nil || !s.Process.Complete() {
			return stopTargetValidation{OK: false, Reason: "missing process start identity"}, nil
		}

		return revalidateProcess(ctx, s)
	}

	stopper, ok := paneStoppers[t.Kind]
	if !ok || stopper.method != t.Method {
		return stopTargetValidation{}, fmt.Errorf("%w: %q", errUnknownStopMethod, t.Method)
	}

	return stopper.validate(ctx, s)
}

func (defaultSessionStopSignaler) SendMultiplexerInterrupt(
	ctx context.Context,
	kind registry.MultiplexerKind,
	serverIdentity string, paneID string,
) error {
	stopper, ok := paneStoppers[kind]
	if !ok {
		return fmt.Errorf("%w: %q", errNoPaneInterrupt, kind)
	}

	if err := stopper.interrupter.SendInterrupt(ctx, serverIdentity, paneID); err != nil {
		return fmt.Errorf("send multiplexer interrupt: %w", err)
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

func (app *application) resolveStopSessions(
	ctx context.Context,
	args []string,
	all bool,
) ([]registry.Session, error) {
	if all {
		listed, err := app.registryStore().
			List(ctx, registry.Filter{Presence: registry.PresenceLive})
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

func runManageStopSessions(
	ctx context.Context,
	ss []registry.Session,
	opts manageStopAllOptions,
) (manageStopAllResult, error) {
	if opts.signaler == nil {
		opts.signaler = defaultSessionStopSignaler{}
	}

	sort.Slice(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
	r := manageStopAllResult{
		DryRun:  opts.dryRun,
		Results: make([]manageStopSessionResult, 0, len(ss)),
	}
	seen := map[string]bool{}

	for _, s := range ss {
		entry := manageStopSessionResult{
			ID:       s.ID,
			Harness:  s.Harness,
			Presence: s.Presence(),
			Activity: s.Activity(),
			Status:   "skipped",
		}
		if s.Presence() != registry.PresenceLive {
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

		v, e := opts.signaler.ValidateStopTarget(ctx, s, t)
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

		if opts.dryRun {
			entry.Status = "would_stop"
			r.Results = append(r.Results, entry)

			continue
		}

		if e = sendStopSignal(ctx, opts.signaler, t); e != nil {
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

	v := tmuxStopTargetValidation(s, panes)
	if !v.OK {
		return v, nil
	}

	return revalidateTmuxForegroundProcess(ctx, s)
}

func revalidateTmuxForegroundProcess(
	ctx context.Context,
	s registry.Session,
) (stopTargetValidation, error) {
	if s.Process == nil || s.Process.PID <= 0 {
		return stopTargetValidation{OK: true}, nil
	}

	if !s.Process.Complete() {
		return stopTargetValidation{OK: false, Reason: "missing process start identity"}, nil
	}

	proc, found, err := processinfo.Find(ctx, s.Process.PID)
	if err != nil {
		return stopTargetValidation{}, fmt.Errorf("find process: %w", err)
	}

	if validation := stopProcessValidation(s, proc, found); !validation.OK {
		return validation, nil
	}

	if !proc.Foreground {
		return stopTargetValidation{OK: false, Reason: "process is not in foreground"}, nil
	}

	return stopTargetValidation{OK: true}, nil
}

func tmuxStopTargetValidation(session registry.Session, panes []tmux.Pane) stopTargetValidation {
	if session.Process != nil && !session.Process.Foreground {
		return stopTargetValidation{OK: false, Reason: "tracked process is not in foreground"}
	}

	paneIDFound := false

	for _, p := range panes {
		if p.Location.PaneID != session.Location.PaneID {
			continue
		}

		paneIDFound = true

		if tmuxTargetMatchesSession(session.Location, p.Location) {
			return stopTargetValidation{OK: true}
		}
	}

	if paneIDFound {
		return stopTargetValidation{OK: false, Reason: "tmux pane identity changed"}
	}

	return stopTargetValidation{OK: false, Reason: "tmux pane no longer exists"}
}

func revalidateProcess(ctx context.Context, s registry.Session) (stopTargetValidation, error) {
	proc, found, err := processinfo.Find(ctx, s.Process.PID)
	if err != nil {
		return stopTargetValidation{}, fmt.Errorf("find process: %w", err)
	}

	return stopProcessValidation(s, proc, found), nil
}

func stopProcessValidation(
	s registry.Session,
	proc processinfo.Process,
	found bool,
) stopTargetValidation {
	if !found || proc.StartIdentity == "" {
		return stopTargetValidation{OK: false, Reason: "process no longer exists"}
	}

	if proc.StartIdentity != s.Process.StartIdentity {
		return stopTargetValidation{OK: false, Reason: "process identity changed"}
	}

	if id, ok := catalog.ResolveProcess(proc); !ok || id != s.Harness {
		return stopTargetValidation{OK: false, Reason: "process command changed"}
	}

	return stopTargetValidation{OK: true}
}

func tmuxTargetMatchesSession(a registry.Location, b registry.Location) bool {
	if a.ServerID == "" || b.ServerID == "" || a.ServerID != b.ServerID {
		return false
	}

	if a.PanePID > 0 && b.PanePID > 0 && a.PanePID != b.PanePID {
		return false
	}

	for _, p := range [][2]string{
		{
			a.SessionID,
			b.SessionID,
		},
		{
			a.SessionName,
			b.SessionName,
		},
		{
			a.WindowID,
			b.WindowID,
		},
		{
			a.WindowIndex,
			b.WindowIndex,
		},
		{
			a.PaneIndex,
			b.PaneIndex,
		},
	} {
		if p[0] != "" && p[1] != "" && p[0] != p[1] {
			return false
		}
	}

	return true
}

func stopTargetForSession(s registry.Session) (stopTarget, bool) {
	if stopper, ok := paneStoppers[s.Location.Kind]; ok &&
		s.Location.PaneID != "" && s.Location.ServerID != "" {
		return stopTarget{
			Method:         stopper.method,
			Target:         s.Location.PaneID,
			Kind:           s.Location.Kind,
			ServerIdentity: s.Location.ServerID,
		}, true
	}

	if s.Process != nil && s.Process.PID > 0 && !sharedHarnessProcess(s.Harness) {
		return stopTarget{
			Method: stopMethodProcess,
			Target: strconv.Itoa(s.Process.PID),
			PID:    s.Process.PID,
		}, true
	}

	return stopTarget{}, false
}

func sharedHarnessProcess(id registry.Harness) bool {
	rules := catalog.Rules{}

	return rules.Known(id) && !rules.Policy(id).ExclusiveProcess
}

func sendStopSignal(ctx context.Context, s sessionStopSignaler, t stopTarget) error {
	if t.Method == stopMethodProcess {
		if err := s.SendProcessInterrupt(t.PID); err != nil {
			return fmt.Errorf("send process interrupt: %w", err)
		}

		return nil
	}

	if err := s.SendMultiplexerInterrupt(ctx, t.Kind, t.ServerIdentity, t.Target); err != nil {
		return fmt.Errorf("send %s interrupt: %w", t.Kind, err)
	}

	return nil
}

func (app *application) writeManageStopAllResult(r manageStopAllResult) error {
	const (
		stopIDWidth       = 16
		stopAgentWidth    = 12
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

		rows = append(
			rows,
			[]string{
				result.ID,
				string(result.Harness),
				string(result.Presence),
				formatActivity(result.Activity),
				result.Status,
				result.Method,
				result.Target,
				detail,
			},
		)
	}

	if err := app.writeWrappedHumanTable(
		[]humanColumn{
			{heading: "ID", width: idWidth, wrap: wrapHumanIdentifier},
			{heading: "Agent", width: stopAgentWidth},
			{heading: "Presence", width: stopPresenceWidth},
			{heading: "Activity", width: stopActivityWidth},
			{heading: "Status", width: stopStatusWidth},
			{heading: "Method", width: stopMethodWidth},
			{heading: "Target", width: targetWidth, wrap: wrapHumanIdentifier},
			{heading: "Detail", width: stopDetailWidth},
		},
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

	return app.writef(
		"Summary: stoppable=%d stopped=%d skipped=%d failed=%d\n",
		r.Stoppable,
		r.Stopped,
		r.Skipped,
		r.Failed,
	)
}

func (app *application) newStopCommand() *cobra.Command {
	all := false
	dryRun := false
	yes := false
	command := &cobra.Command{
		Use:           "stop [session...]",
		Short:         "Gracefully stop sessions",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return exitCode(errStopSelection, exitCodeUsage)
			}

			if !all && len(args) == 0 {
				return exitCode(errStopSelection, exitCodeUsage)
			}

			if all && !yes && !dryRun {
				stdin := app.stdin
				if stdin == nil {
					stdin = os.Stdin
				}

				confirmed, err := app.confirmStopAll(cmd.Context(), stdin)
				if err != nil {
					return err
				}

				if !confirmed {
					return exitCode(errStopAllConfirmation, exitCodeGeneral)
				}
			}

			return app.runStop(cmd.Context(), args, all, dryRun)
		},
	}
	command.Flags().BoolVarP(&all, "all", "a", false, "stop every live session")
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show targets without sending signals")
	command.Flags().
		BoolVarP(&yes, "yes", "y", false, "confirm stopping all sessions without prompting")

	return command
}
