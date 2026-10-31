//go:build compatibility

package interactive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
	"github.com/zigai/aht/v2/pkg/registry"
)

const interactiveReply = "aht fakeprovider interactive reply"

func TestCurrentHarnessInteractive(t *testing.T) {
	harnessID := registry.Harness(strings.TrimSpace(os.Getenv("AHT_COMPAT_HARNESS")))
	if harnessID == "" {
		t.Fatal("AHT_COMPAT_HARNESS is required; use `just compatibility <harness>`")
	}

	provider := startProvider(t)

	launched, err := launch.New(t.Context(), launch.Options{
		Harness:    harnessID,
		Root:       filepath.Join(t.TempDir(), "root"),
		Endpoint:   provider.URL(),
		Model:      "",
		APIKey:     "",
		AHT:        buildAHT(t),
		Executable: "",
		Env:        nil,
	})
	if errors.Is(err, launch.ErrUnsupported) {
		t.Logf("%s has no launch plan: no interactive session claim", harnessID)
		return
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := launched.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := launched.StartTracker(t.Context()); err != nil {
		t.Fatal(err)
	}

	terminal, err := launched.StartTerminal(t.Context(), "", gotmux.Size{Width: 160, Height: 45})
	if errors.Is(err, launch.ErrUnsupported) {
		t.Logf("%s has no interactive launch: no interactive session claim", harnessID)
		return
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			logFailure(t, terminal, provider)
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()

		if err := terminal.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	for turn, prompt := range []string{"aht fakeprovider first prompt", "aht fakeprovider second prompt"} {
		runTurn(t, launched, terminal, provider, turn, prompt)
	}
}

func startProvider(t *testing.T) *fakeprovider.Server {
	t.Helper()

	provider, err := fakeprovider.Start(fakeprovider.Options{
		Address:     "",
		Model:       "",
		Hold:        true,
		SideMarkers: nil,
		Script: fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
			return fakeprovider.Reply{
				Text:          interactiveReply,
				ToolCalls:     nil,
				Error:         nil,
				HoldMidStream: false,
				Disconnect:    false,
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})

	return provider
}

func runTurn(
	t *testing.T,
	launched *launch.Harness,
	terminal *launch.Terminal,
	provider *fakeprovider.Server,
	turn int,
	prompt string,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	if err := submitPrompt(ctx, terminal, prompt); err != nil {
		t.Fatalf("turn %d: %v", turn, err)
	}

	var hold *fakeprovider.Hold
	select {
	case hold = <-provider.Holds():
	case <-ctx.Done():
		t.Fatalf("turn %d: no model request after submitting %q", turn, prompt)
	}

	if !strings.Contains(string(hold.Request.Body), prompt) {
		t.Fatalf(
			"turn %d: model request does not contain %q; last user text %q",
			turn,
			prompt,
			hold.Request.UserText,
		)
	}

	if _, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		return nativeActivity(session, registry.ActivityRunning) &&
			effectiveActivity(session, registry.ActivityRunning)
	}); err != nil {
		t.Fatalf("turn %d: held turn not observed as running: %v", turn, err)
	}

	hold.Release()

	if _, err := terminal.WaitForText(ctx, interactiveReply); err != nil {
		t.Fatalf("turn %d: reply not rendered: %v", turn, err)
	}

	if _, err := launched.WaitForSession(ctx, func(session registry.Session) bool {
		return nativeActivity(session, registry.ActivityIdle) &&
			effectiveActivity(session, registry.ActivityIdle)
	}); err != nil {
		t.Fatalf("turn %d: completed turn not observed as idle: %v", turn, err)
	}

	if err := provider.Err(); err != nil {
		t.Fatalf("turn %d: provider: %v", turn, err)
	}

	if got := len(provider.Requests()); got != turn+1 {
		t.Fatalf("turn %d: conversation requests = %d, want %d", turn, got, turn+1)
	}
}

func submitPrompt(ctx context.Context, terminal *launch.Terminal, prompt string) error {
	if err := waitForStableScreen(ctx, terminal); err != nil {
		return err
	}

	for {
		if err := terminal.SendText(ctx, prompt); err != nil {
			return fmt.Errorf("type prompt: %w", err)
		}

		waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
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
	const settle = 2 * time.Second

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
		} else if strings.TrimSpace(screen) != "" && time.Since(changed) >= settle {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("screen did not settle: %w; last capture:\n%s", ctx.Err(), screen)
		case <-time.After(100 * time.Millisecond):
		}
	}
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

func buildAHT(t *testing.T) string {
	t.Helper()

	if binary := os.Getenv("AHT_COMPAT_BINARY"); binary != "" {
		path, err := filepath.Abs(binary)
		if err != nil {
			t.Fatal(err)
		}

		return path
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating test source")
	}

	output := filepath.Join(t.TempDir(), "aht")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, "go", "build", "-o", output, ".")

	command.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build aht: %v\n%s", err, out)
	}

	return output
}

func logFailure(t *testing.T, terminal *launch.Terminal, provider *fakeprovider.Server) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
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
