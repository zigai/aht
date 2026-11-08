package launch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionsKeepsWorkSessionsAndDropsRawPayload(t *testing.T) {
	t.Parallel()
	sessionsPath := filepath.Join(t.TempDir(), "sessions.json")
	options := Options{
		Root: t.TempDir(),
		AHT:  fakeAHT(t),
		Env:  []string{sessionsFileEnv + "=" + sessionsPath},
	}

	s, _, err := newFixtureHarness(t.Context(), t, Plan{}, options)
	if err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := writeSessions(sessionsPath,
		sessionFixture("s-work", s.Work, "native-work"),
		sessionFixture("s-other", other, "native-other"),
	); err != nil {
		t.Fatal(err)
	}

	sessions, err := s.Sessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].ID != "s-work" {
		t.Fatalf("Sessions() = %+v, want only the session in Work", sessions)
	}

	native := sessions[0].Observations.Native
	if native == nil || native.RawPayload != nil {
		t.Fatalf("native observation = %+v, want its raw payload removed", native)
	}
}

func TestWaitForSessionReturnsWhenSessionAppears(t *testing.T) {
	t.Parallel()
	sessionsPath := filepath.Join(t.TempDir(), "sessions.json")
	options := Options{
		Root: t.TempDir(),
		AHT:  fakeAHT(t),
		Env:  []string{sessionsFileEnv + "=" + sessionsPath},
	}

	s, _, err := newFixtureHarness(t.Context(), t, Plan{}, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeSessions(
		sessionsPath,
		sessionFixture("s-pending", s.Work, "native-pending"),
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	got, err := s.WaitForSession(ctx, func(session registry.Session) bool {
		if session.ID == "s-pending" {
			if err := writeSessions(
				sessionsPath,
				sessionFixture("s-late", s.Work, "native-late"),
			); err != nil {
				t.Fatal(err)
			}

			return false
		}

		return session.ID == "s-late"
	})
	if err != nil {
		t.Fatalf("WaitForSession() error = %v", err)
	}

	if got.ID != "s-late" {
		t.Fatalf("WaitForSession() = %q, want s-late", got.ID)
	}
}

func TestWaitForSessionTimeoutReportsLastSessions(t *testing.T) {
	t.Parallel()
	sessionsPath := filepath.Join(t.TempDir(), "sessions.json")
	options := Options{
		Root: t.TempDir(),
		AHT:  fakeAHT(t),
		Env:  []string{sessionsFileEnv + "=" + sessionsPath},
	}

	s, _, err := newFixtureHarness(t.Context(), t, Plan{}, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeSessions(
		sessionsPath,
		sessionFixture("s-idle", s.Work, "native-idle"),
	); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err = s.WaitForSession(ctx, func(registry.Session) bool {
		cancel()
		return false
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForSession() error = %v, want context.Canceled", err)
	}

	if !strings.Contains(err.Error(), "s-idle") {
		t.Fatalf("timeout error %q does not include the last sessions", err)
	}
}
