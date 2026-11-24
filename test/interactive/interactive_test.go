//go:build compatibility && harness

package interactive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
)

const (
	interactiveReply   = "aht fakeprovider interactive reply"
	badRequestMessage  = "aht fakeprovider bad request"
	terminalWidth      = 160
	terminalHeight     = 45
	turnTimeout        = 90 * time.Second
	screenTimeout      = 30 * time.Second
	closeTimeout       = 10 * time.Second
	failureLogTimeout  = 5 * time.Second
	promptEchoTimeout  = 3 * time.Second
	screenSettleWindow = 2 * time.Second
	screenPollInterval = 100 * time.Millisecond
)

var errSessionMissing = errors.New("session no longer present in store")

type replyScript struct {
	mu    sync.Mutex
	reply fakeprovider.Reply
}

func newReplyScript() *replyScript {
	return &replyScript{reply: defaultTextReply()}
}

func (s *replyScript) Set(reply fakeprovider.Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reply = reply
}

func (s *replyScript) Reply(fakeprovider.Request) (fakeprovider.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reply, nil
}

func defaultTextReply() fakeprovider.Reply {
	return fakeprovider.Reply{
		Text:          interactiveReply,
		ToolCalls:     nil,
		Error:         nil,
		HoldMidStream: false,
		Disconnect:    false,
	}
}

func TestCurrentHarnessInteractive(t *testing.T) {
	harnessID := registry.Harness(strings.TrimSpace(os.Getenv("AHT_COMPAT_HARNESS")))
	if harnessID == "" {
		t.Fatal("AHT_COMPAT_HARNESS is required; use `just compatibility <harness>`")
	}

	adapter, ok := adapters.Find(harnessID)
	if !ok {
		t.Fatalf("unknown harness %q", harnessID)
	}

	def := adapter.Definition()
	provider, script := startProvider(t, harnessID)

	launched, err := launch.Open(t.Context(), launch.Options{
		Harness:    harnessID,
		Root:       filepath.Join(t.TempDir(), "root"),
		Endpoint:   provider.URL(),
		Model:      "",
		APIKey:     "",
		AHT:        testharness.Build(t),
		Executable: "",
		Env:        nil,
	})
	if errors.Is(err, launch.ErrUnsupported) {
		t.Skipf("%s has no launch plan: no interactive session claim", harnessID)
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := launched.Close(t.Context()); err != nil {
			t.Error(err)
		}
	})

	if err := launched.StartTracker(t.Context()); err != nil {
		t.Fatal(err)
	}

	terminal, err := launched.StartTerminal(
		t.Context(),
		"",
		gotmux.Size{Width: terminalWidth, Height: terminalHeight},
	)
	if errors.Is(err, launch.ErrUnsupported) {
		t.Skipf("%s has no interactive launch: no interactive session claim", harnessID)
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			logFailure(t, terminal, provider)
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), closeTimeout)
		defer cancel()

		if err := terminal.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	t.Run("turns", func(t *testing.T) {
		runTurnsSubtest(t, def, launched, terminal, provider, script)
	})

	t.Run("stock_default_screen", func(t *testing.T) {
		runStockDefaultScreenSubtest(t, def, terminal)
	})

	t.Run("escape_interrupt", func(t *testing.T) {
		runEscapeInterruptSubtest(t, def, launched, terminal, provider, script)
	})

	t.Run("provider_error", func(t *testing.T) {
		runProviderErrorSubtest(t, def, launched, terminal, provider, script)
	})
}

func startProvider(t *testing.T, id registry.Harness) (*fakeprovider.Server, *replyScript) {
	t.Helper()

	titleRequest, err := launch.TitleRequestFor(id)
	if errors.Is(err, launch.ErrUnsupported) {
		t.Skipf("%s has no launch plan: no interactive session claim", id)
	}

	if err != nil {
		t.Fatal(err)
	}

	script := newReplyScript()

	provider, err := fakeprovider.Start(fakeprovider.Options{
		Address:     "",
		Model:       "",
		Hold:        true,
		SideRequest: titleRequest,
		Script:      script,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})

	return provider, script
}

func runTurnsSubtest(
	t *testing.T,
	def harness.Definition,
	launched *launch.Harness,
	terminal *launch.Terminal,
	provider *fakeprovider.Server,
	script *replyScript,
) {
	t.Helper()

	script.Set(defaultTextReply())

	prompts := []string{"aht fakeprovider first prompt", "aht fakeprovider second prompt"}
	for turn, prompt := range prompts {
		runTurn(t, def, launched, terminal, provider, turn, prompt)
	}
}

func runTurn(
	t *testing.T,
	def harness.Definition,
	launched *launch.Harness,
	terminal *launch.Terminal,
	provider *fakeprovider.Server,
	turn int,
	prompt string,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), turnTimeout)
	defer cancel()

	if err := submitPrompt(ctx, terminal, prompt); err != nil {
		t.Fatalf("turn %d: %v", turn, err)
	}

	hold, err := testharness.AwaitHold(ctx, provider, nil)
	if err != nil {
		t.Fatalf("turn %d: no model request after submitting %q: %v", turn, prompt, err)
	}
	defer hold.Release()

	if !strings.Contains(string(hold.Request.Body), prompt) {
		t.Fatalf(
			"turn %d: model request does not contain %q; last user text %q",
			turn,
			prompt,
			hold.Request.UserText,
		)
	}

	runningSession, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		return observedActivity(def, session, registry.ActivityRunning)
	})
	if err != nil {
		t.Fatalf("turn %d: held turn not observed as running: %v", turn, err)
	}

	verifyTerminalStatus(t, ctx, def, launched, terminal, runningSession, registry.ActivityRunning)

	hold.Release()

	if _, err := terminal.WaitForText(ctx, interactiveReply); err != nil {
		t.Fatalf("turn %d: reply not rendered: %v", turn, err)
	}

	idleSession, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		return observedActivity(def, session, registry.ActivityIdle)
	})
	if err != nil {
		t.Fatalf("turn %d: completed turn not observed as idle: %v", turn, err)
	}

	verifyTerminalStatus(t, ctx, def, launched, terminal, idleSession, registry.ActivityIdle)

	if err := provider.Err(); err != nil {
		t.Fatalf("turn %d: provider: %v", turn, err)
	}

	if got := len(provider.Requests()); got != turn+1 {
		t.Fatalf("turn %d: conversation requests = %d, want %d", turn, got, turn+1)
	}
}

func runStockDefaultScreenSubtest(
	t *testing.T,
	def harness.Definition,
	terminal *launch.Terminal,
) {
	t.Helper()

	if def.ScreenManifest == "" {
		t.Skipf("%s has no bundled screen manifest", def.ID)
	}

	ctx, cancel := context.WithTimeout(t.Context(), screenTimeout)
	defer cancel()

	if err := waitForStableScreen(ctx, terminal); err != nil {
		t.Fatalf("idle screen did not settle: %v", err)
	}

	screen, err := terminal.Capture(ctx)
	if err != nil {
		t.Fatalf("capture idle screen: %v", err)
	}

	manifest, err := agentstate.ParseManifest([]byte(def.ScreenManifest), def.ID)
	if err != nil {
		t.Fatalf("parse bundled screen manifest for %s: %v", def.ID, err)
	}

	decision := manifest.Evaluate(agentstate.NormalizeSnapshot(screen, ""))
	if decision.Activity != registry.ActivityIdle {
		t.Fatalf(
			"stock default idle screen evaluated to %q (reason=%q rule=%q), want %q; screen:\n%s",
			decision.Activity,
			decision.Reason,
			decision.RuleID,
			registry.ActivityIdle,
			screen,
		)
	}
}

func runEscapeInterruptSubtest(
	t *testing.T,
	def harness.Definition,
	launched *launch.Harness,
	terminal *launch.Terminal,
	provider *fakeprovider.Server,
	script *replyScript,
) {
	t.Helper()

	const prompt = "aht fakeprovider interrupt prompt"

	script.Set(defaultTextReply())

	ctx, cancel := context.WithTimeout(t.Context(), turnTimeout)
	defer cancel()

	if err := submitPrompt(ctx, terminal, prompt); err != nil {
		t.Fatalf("submit interrupt prompt: %v", err)
	}

	hold, err := testharness.AwaitHold(ctx, provider, nil)
	if err != nil {
		t.Fatalf("no model request after submitting %q: %v", prompt, err)
	}
	defer hold.Release()

	if !strings.Contains(string(hold.Request.Body), prompt) {
		t.Fatalf(
			"model request does not contain %q; last user text %q",
			prompt,
			hold.Request.UserText,
		)
	}

	runningSession, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		return observedActivity(def, session, registry.ActivityRunning)
	})
	if err != nil {
		t.Fatalf("held turn not observed as running before interrupt: %v", err)
	}

	hasTerminalStatus := terminalObservation(runningSession) != nil ||
		paneHasProgramStatus(ctx, terminal, def.ProgramStatusApp)

	if err := terminal.SendKeys(ctx, gotmux.KeyEscape); err != nil {
		t.Fatalf("send escape to interrupt turn: %v", err)
	}

	waitCtx := ctx

	if def.ProgramStatusApp != "" && !hasTerminalStatus {
		var cancelWait context.CancelFunc

		waitCtx, cancelWait = context.WithTimeout(ctx, 10*time.Second)
		defer cancelWait()
	}

	session, err := launched.WaitForSession(waitCtx, func(session registry.Session) bool {
		return effectiveActivity(session, registry.ActivityInterrupted) ||
			effectiveActivity(session, registry.ActivityIdle)
	})
	if err != nil {
		if def.ProgramStatusApp != "" && !hasTerminalStatus {
			_ = terminal.SendKeys(ctx, gotmux.Key("C-u"))

			t.Skipf(
				"tmux lacks OSC 7501 program status and %s emitted no native hook on Escape",
				def.ID,
			)
		}

		t.Fatalf("turn did not leave running after Escape: %v", err)
	}

	if err := waitForStableScreen(ctx, terminal); err != nil {
		t.Fatalf("post-interrupt screen did not settle: %v", err)
	}

	settled, err := sessionByID(ctx, launched, session.ID)
	if err != nil {
		t.Fatalf("read settled session after interrupt: %v", err)
	}

	if nativeActivity(settled, registry.ActivityInterrupted) {
		if !effectiveActivity(settled, registry.ActivityInterrupted) {
			t.Fatalf(
				"native interrupted activity was overwritten: activity=%v decision=%#v screen=%#v",
				settled.Activity(),
				settled.Decision(),
				settled.Observations.Screen,
			)
		}
	} else if !effectiveActivity(settled, registry.ActivityIdle) &&
		!effectiveActivity(settled, registry.ActivityInterrupted) {
		t.Fatalf(
			"post-interrupt activity = %v (decision=%#v), want %q or %q",
			settled.Activity(),
			settled.Decision(),
			registry.ActivityIdle,
			registry.ActivityInterrupted,
		)
	}

	if err := provider.Err(); err != nil {
		t.Fatalf("provider after interrupt: %v", err)
	}

	// Clear any interrupted prompt text restored into the input box before the next subtest.
	_ = terminal.SendKeys(ctx, gotmux.Key("C-u"))
}

func runProviderErrorSubtest(
	t *testing.T,
	def harness.Definition,
	launched *launch.Harness,
	terminal *launch.Terminal,
	provider *fakeprovider.Server,
	script *replyScript,
) {
	t.Helper()

	if def.StateAuthority == registry.AuthorityScreen &&
		!manifestHasFailedRule(def.ScreenManifest, def.ID) {
		t.Skipf("%s screen manifest has no failed rules", def.ID)
	}

	const prompt = "aht fakeprovider error prompt"

	script.Set(fakeprovider.Reply{
		Text:      "",
		ToolCalls: nil,
		Error: &fakeprovider.ErrorReply{
			Status:  http.StatusBadRequest,
			Message: badRequestMessage,
		},
		HoldMidStream: false,
		Disconnect:    false,
	})

	defer script.Set(defaultTextReply())

	ctx, cancel := context.WithTimeout(t.Context(), turnTimeout)
	defer cancel()

	if err := submitPrompt(ctx, terminal, prompt); err != nil {
		t.Fatalf("submit error prompt: %v", err)
	}

	hold, err := testharness.AwaitHold(ctx, provider, nil)
	if err != nil {
		t.Fatalf("no model request after submitting %q: %v", prompt, err)
	}
	defer hold.Release()

	if !strings.Contains(string(hold.Request.Body), prompt) {
		t.Fatalf(
			"model request does not contain %q; last user text %q",
			prompt,
			hold.Request.UserText,
		)
	}

	if _, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		return observedActivity(def, session, registry.ActivityRunning)
	}); err != nil {
		t.Fatalf("held turn not observed as running before provider error: %v", err)
	}

	hold.Release()

	// Release any fallback or retry request a harness issues after the first 400 error.
	drainCtx, stopDrain := context.WithCancel(ctx)
	drainDone := make(chan struct{})

	go func() {
		defer close(drainDone)

		for {
			select {
			case <-drainCtx.Done():
				return
			case retryHold, ok := <-provider.Holds():
				if !ok {
					return
				}

				retryHold.Release()
			}
		}
	}()

	defer func() {
		stopDrain()
		<-drainDone
	}()

	requireFailed := expectsFailedOnProviderError(def)

	if _, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		if requireFailed {
			return effectiveActivity(session, registry.ActivityFailed)
		}

		return effectiveActivity(session, registry.ActivityFailed) ||
			effectiveActivity(session, registry.ActivityIdle)
	}); err != nil {
		t.Fatalf(
			"session did not reflect provider error (requireFailed=%t): %v",
			requireFailed,
			err,
		)
	}

	if err := provider.Err(); err != nil {
		t.Fatalf("provider after error reply: %v", err)
	}
}

func verifyTerminalStatus(
	t *testing.T,
	ctx context.Context,
	def harness.Definition,
	launched *launch.Harness,
	terminal *launch.Terminal,
	session registry.Session,
	want registry.Activity,
) {
	t.Helper()

	if def.ProgramStatusApp == "" {
		return
	}

	if terminalObservation(session) == nil &&
		!paneHasProgramStatus(ctx, terminal, def.ProgramStatusApp) {
		return
	}

	if terminalActivity(session, want) {
		return
	}

	if _, err := launched.WaitForSession(ctx, func(candidate registry.Session) bool {
		return terminalActivity(candidate, want) && effectiveActivity(candidate, want)
	}); err != nil {
		t.Fatalf("OSC 7501 terminal authority did not observe %q: %v", want, err)
	}
}

func paneHasProgramStatus(ctx context.Context, terminal *launch.Terminal, app string) bool {
	if app == "" {
		return false
	}

	panes, err := tmux.ListPanesWithOptions(ctx, tmux.ListOptions{
		SocketPaths: []string{terminal.Socket()},
		Processes:   []mux.Process{},
	})
	if err != nil {
		return false
	}

	for _, pane := range panes {
		if pane.Program == nil || pane.Program.App != app {
			continue
		}

		if _, _, valid := pane.Program.Activity(); valid {
			return true
		}
	}

	return false
}

func expectsFailedOnProviderError(def harness.Definition) bool {
	return def.ProgramStatusApp != "" ||
		def.DetailCapabilities.Native.UsageLimit ||
		def.StateAuthority == registry.AuthorityScreen
}

func manifestHasFailedRule(raw string, id registry.Harness) bool {
	if raw == "" {
		return false
	}

	manifest, err := agentstate.ParseManifest([]byte(raw), id)
	if err != nil {
		return false
	}

	for _, rule := range manifest.Rules {
		activity, err := registry.ParseActivity(rule.State)
		if err == nil && activity == registry.ActivityFailed {
			return true
		}
	}

	return false
}

func sessionByID(
	ctx context.Context,
	launched *launch.Harness,
	id string,
) (registry.Session, error) {
	sessions, err := launched.Sessions(ctx)
	if err != nil {
		return registry.Session{}, err
	}

	for _, session := range sessions {
		if session.ID == id {
			return session, nil
		}
	}

	return registry.Session{}, fmt.Errorf("%w: %s", errSessionMissing, id)
}

func submitPrompt(ctx context.Context, terminal *launch.Terminal, prompt string) error {
	if err := waitForStableScreen(ctx, terminal); err != nil {
		return err
	}

	for {
		if err := terminal.SendText(ctx, prompt); err != nil {
			return fmt.Errorf("type prompt: %w", err)
		}

		waitCtx, cancel := context.WithTimeout(ctx, promptEchoTimeout)
		_, err := terminal.WaitForText(waitCtx, prompt)

		cancel()

		if err == nil {
			if err := terminal.SendKeys(ctx, gotmux.KeyEnter); err != nil {
				return fmt.Errorf("submit prompt: %w", err)
			}

			return nil
		}

		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}

		if err := terminal.SendKeys(ctx, gotmux.Key("C-u")); err != nil {
			return fmt.Errorf("clear prompt: %w", err)
		}
	}
}

func waitForStableScreen(ctx context.Context, terminal *launch.Terminal) error {
	previous := ""
	changed := time.Now()

	for {
		screen, err := terminal.Capture(ctx)
		if err != nil {
			return fmt.Errorf("capture screen: %w", err)
		}

		if screen != previous {
			previous = screen
			changed = time.Now()
		} else if strings.TrimSpace(screen) != "" && time.Since(changed) >= screenSettleWindow {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("screen did not settle: %w; last capture:\n%s", ctx.Err(), screen)
		case <-time.After(screenPollInterval):
		}
	}
}

func observedActivity(
	def harness.Definition,
	session registry.Session,
	want registry.Activity,
) bool {
	if def.StateAuthority == registry.AuthorityHook && !nativeActivity(session, want) {
		return false
	}

	return effectiveActivity(session, want)
}

func terminalObservation(session registry.Session) *registry.ScreenObservation {
	screen := session.Observations.Screen
	if screen == nil || screen.Authority != registry.AuthorityTerminal {
		return nil
	}

	return screen
}

func terminalActivity(session registry.Session, want registry.Activity) bool {
	screen := terminalObservation(session)
	return screen != nil && screen.Activity == want
}

func nativeActivity(session registry.Session, want registry.Activity) bool {
	native := session.Observations.Native
	return native != nil && native.Activity != nil && *native.Activity == want
}

func effectiveActivity(session registry.Session, want registry.Activity) bool {
	activity := session.Activity()
	if activity != nil && *activity == want {
		return true
	}

	decision := session.Decision()

	return activity != nil && *activity == registry.ActivityUnknown && decision != nil &&
		decision.Authority == "screen" && decision.Reason == "screen_not_in_supported_multiplexer"
}

func logFailure(t *testing.T, terminal *launch.Terminal, provider *fakeprovider.Server) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), failureLogTimeout)
	defer cancel()

	screen, err := terminal.Capture(ctx)
	t.Logf("presented screen (capture error: %v):\n%s", err, screen)

	for _, request := range provider.Requests() {
		t.Logf(
			"request %d %s %s outcome=%s user=%q",
			request.Index,
			request.Protocol,
			request.Path,
			request.Outcome,
			request.UserText,
		)
	}

	for _, request := range provider.SideRequests() {
		t.Logf("side request %s %s outcome=%s", request.Protocol, request.Path, request.Outcome)
	}
}
