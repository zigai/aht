package client

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestCurrentAncestorLookup(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})

	agentPID := 100
	agentIdentity := "boot-1:100:agent-start"

	live := registry.PresenceLive
	activity := registry.ActivityRunning
	obs := registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-ancestor-1"}, Evidence: &registry.Report{Lifecycle: nil, Claim: &live, Activity: &activity, Process: &registry.ProcessIdentity{
		PID:            agentPID,
		PPID:           1,
		ProcessGroupID: 100,
		Foreground:     true,
		StartIdentity:  agentIdentity,
		Executable:     "/usr/bin/codex",
		CWD:            "/tmp/work",
		TTY:            "/dev/pts/1",
	}, Location: nil, Listing: nil, Attributes: nil, Payload: nil}}

	createdSession, err := store.Observe(t.Context(), obs)
	if err != nil {
		t.Fatal(err)
	}

	c := New(Config{
		StorePath:  storePath,
		SocketPath: "",
		Mode:       ModeDurableOnly,
	})

	// Process tree: Caller (PID 300) -> Parent (PID 200, bash) -> Grandparent (PID 100, codex)
	processTree := []processinfo.Process{
		{
			PID:                300,
			PPID:               200,
			ProcessGroupID:     100,
			Foreground:         true,
			StartIdentity:      "boot-1:300:caller",
			Executable:         "/usr/bin/aht",
			CWD:                "/tmp/work",
			TTY:                "/dev/pts/1",
			AgentHint:          "",
			MultiplexerKind:    "",
			MultiplexerServer:  "",
			MultiplexerSession: "",
			MultiplexerPane:    "",
			Args:               nil,
		},
		{
			PID:                200,
			PPID:               agentPID,
			ProcessGroupID:     100,
			Foreground:         true,
			StartIdentity:      "boot-1:200:shell",
			Executable:         "/bin/bash",
			CWD:                "/tmp/work",
			TTY:                "/dev/pts/1",
			AgentHint:          "",
			MultiplexerKind:    "",
			MultiplexerServer:  "",
			MultiplexerSession: "",
			MultiplexerPane:    "",
			Args:               nil,
		},
		{
			PID:                agentPID,
			PPID:               1,
			ProcessGroupID:     100,
			Foreground:         true,
			StartIdentity:      agentIdentity,
			Executable:         "/usr/bin/codex",
			CWD:                "/tmp/work",
			TTY:                "/dev/pts/1",
			AgentHint:          "",
			MultiplexerKind:    "",
			MultiplexerServer:  "",
			MultiplexerSession: "",
			MultiplexerPane:    "",
			Args:               nil,
		},
	}

	resolved, err := c.currentWithInspectors(t.Context(), currentInspectors{
		PID: 300,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return processTree, nil
		},
		ProcessFind: func(_ context.Context, pid int) (processinfo.Process, bool, error) {
			for _, p := range processTree {
				if p.PID == pid {
					return p, true, nil
				}
			}
			return processinfo.Process{}, false, nil
		},
		TmuxCurrent:   nil,
		ZellijCurrent: nil,
		HerdrCurrent:  nil,
	})
	if err != nil {
		t.Fatalf("ancestor lookup failed: %v", err)
	}
	if resolved.ID != createdSession.ID {
		t.Fatalf("resolved session ID = %q, want %q", resolved.ID, createdSession.ID)
	}
}

func TestCurrentPIDReuseDefense(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})

	agentPID := 500
	originalIdentity := "boot-1:500:original-start"

	live := registry.PresenceLive
	activity := registry.ActivityRunning
	obs := registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-reused-1"}, Evidence: &registry.Report{Lifecycle: nil, Claim: &live, Activity: &activity, Process: &registry.ProcessIdentity{
		PID:            agentPID,
		PPID:           1,
		ProcessGroupID: 500,
		Foreground:     true,
		StartIdentity:  originalIdentity,
		Executable:     "/usr/bin/claude",
		CWD:            "/tmp/work",
		TTY:            "/dev/pts/2",
	}, Location: nil, Listing: nil, Attributes: nil, Payload: nil}}

	if _, err := store.Observe(t.Context(), obs); err != nil {
		t.Fatal(err)
	}

	c := New(Config{
		StorePath:  storePath,
		SocketPath: "",
		Mode:       ModeDurableOnly,
	})

	// Process table has a process with PID 500, but DIFFERENT StartIdentity (PID was reused)
	reusedProcessTree := []processinfo.Process{
		{
			PID:                agentPID,
			PPID:               1,
			ProcessGroupID:     500,
			Foreground:         true,
			StartIdentity:      "boot-1:500:reused-start", // mismatch!
			Executable:         "/usr/bin/other-app",
			CWD:                "/tmp/work",
			TTY:                "/dev/pts/2",
			AgentHint:          "",
			MultiplexerKind:    "",
			MultiplexerServer:  "",
			MultiplexerSession: "",
			MultiplexerPane:    "",
			Args:               nil,
		},
	}

	_, err := c.currentWithInspectors(t.Context(), currentInspectors{
		PID: agentPID,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return reusedProcessTree, nil
		},
		ProcessFind: func(_ context.Context, pid int) (processinfo.Process, bool, error) {
			for _, p := range reusedProcessTree {
				if p.PID == pid {
					return p, true, nil
				}
			}
			return processinfo.Process{}, false, nil
		},
		TmuxCurrent:   nil,
		ZellijCurrent: nil,
		HerdrCurrent:  nil,
	})
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Fatalf("expected ErrNoCurrentSession on PID reuse, got: %v", err)
	}
}

func TestCurrentTerminalContext(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})

	live := registry.PresenceLive
	activity := registry.ActivityRunning
	obs := registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-tmux-pane"}, Evidence: &registry.Report{Lifecycle: nil, Claim: &live, Activity: &activity, Process: &registry.ProcessIdentity{
		PID:            700,
		PPID:           1,
		ProcessGroupID: 700,
		Foreground:     true,
		StartIdentity:  "boot-1:700:tmux-agent",
		Executable:     "/usr/bin/claude",
		CWD:            "/tmp/work",
		TTY:            "/dev/pts/3",
	}, Location: &registry.Location{
		Kind:            registry.MultiplexerTmux,
		ServerID:        "/tmp/tmux.sock",
		SessionID:       "$1",
		SessionName:     "main",
		WorkspaceID:     "",
		WorkspaceName:   "",
		TabID:           "",
		TabIndex:        "",
		TabName:         "",
		WindowID:        "@1",
		WindowIndex:     "1",
		WindowName:      "win",
		PaneID:          "%10",
		PaneIndex:       "0",
		PaneCurrentPath: "/tmp/work",
		PanePID:         700,
		PaneTTY:         "/dev/pts/3",
		ClientTTY:       "",
	}, Listing: nil, Attributes: nil, Payload: nil}}

	createdSession, err := store.Observe(t.Context(), obs)
	if err != nil {
		t.Fatal(err)
	}

	c := New(Config{
		StorePath:  storePath,
		SocketPath: "",
		Mode:       ModeDurableOnly,
	})

	// 1. Success matching verified Tmux terminal pane
	resolved, err := c.currentWithInspectors(t.Context(), currentInspectors{
		PID: 999, // Unrelated caller PID
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return nil, nil // No ancestor match
		},
		ProcessFind: func(_ context.Context, pid int) (processinfo.Process, bool, error) {
			if pid == 700 || pid == 999 {
				return processinfo.Process{
					PID:                pid,
					PPID:               1,
					ProcessGroupID:     700,
					Foreground:         true,
					StartIdentity:      "boot-1:700:tmux-agent",
					Executable:         "/usr/bin/claude",
					CWD:                "/tmp/work",
					TTY:                "/dev/pts/3",
					AgentHint:          "",
					MultiplexerKind:    "",
					MultiplexerServer:  "",
					MultiplexerSession: "",
					MultiplexerPane:    "",
					Args:               nil,
				}, true, nil
			}
			return processinfo.Process{}, false, nil
		},
		TmuxCurrent: func(context.Context) (registry.Location, error) {
			return registry.Location{
				Kind:            registry.MultiplexerTmux,
				ServerID:        "/tmp/tmux.sock",
				SessionID:       "$1",
				SessionName:     "main",
				WindowID:        "@1",
				WindowIndex:     "1",
				WindowName:      "win",
				PaneID:          "%10",
				PaneIndex:       "0",
				PaneCurrentPath: "/tmp/work",
				PanePID:         700,
				PaneTTY:         "/dev/pts/3",
				ClientTTY:       "",
			}, nil
		},
		ZellijCurrent: nil,
		HerdrCurrent:  nil,
	})
	if err != nil {
		t.Fatalf("tmux terminal context resolution failed: %v", err)
	}
	if resolved.ID != createdSession.ID {
		t.Fatalf("resolved session ID = %q, want %q", resolved.ID, createdSession.ID)
	}

	// 2. Stale terminal environment: TMUX_PANE points to a pane with no session
	_, err = c.currentWithInspectors(t.Context(), currentInspectors{
		PID: 999,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return nil, nil
		},
		ProcessFind: nil,
		TmuxCurrent: func(context.Context) (registry.Location, error) {
			return registry.Location{
				Kind:            registry.MultiplexerTmux,
				ServerID:        "/tmp/tmux.sock",
				SessionID:       "$1",
				SessionName:     "main",
				WindowID:        "@1",
				WindowIndex:     "1",
				WindowName:      "win",
				PaneID:          "%999", // non-existent pane
				PaneIndex:       "0",
				PaneCurrentPath: "/tmp/work",
				PanePID:         0,
				PaneTTY:         "",
				ClientTTY:       "",
			}, nil
		},
		ZellijCurrent: nil,
		HerdrCurrent:  nil,
	})
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Fatalf("expected ErrNoCurrentSession for stale pane, got: %v", err)
	}

	// 3. Stale session: process died in pane
	_, err = c.currentWithInspectors(t.Context(), currentInspectors{
		PID: 999,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return nil, nil
		},
		ProcessFind: func(_ context.Context, pid int) (processinfo.Process, bool, error) {
			return processinfo.Process{}, false, nil // process not found (died)
		},
		TmuxCurrent: func(context.Context) (registry.Location, error) {
			return registry.Location{
				Kind:            registry.MultiplexerTmux,
				ServerID:        "/tmp/tmux.sock",
				SessionID:       "$1",
				SessionName:     "main",
				WindowID:        "@1",
				WindowIndex:     "1",
				WindowName:      "win",
				PaneID:          "%10",
				PaneIndex:       "0",
				PaneCurrentPath: "/tmp/work",
				PanePID:         700,
				PaneTTY:         "/dev/pts/3",
				ClientTTY:       "",
			}, nil
		},
		ZellijCurrent: nil,
		HerdrCurrent:  nil,
	})
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Fatalf("expected ErrNoCurrentSession when process died in pane, got: %v", err)
	}
}

func TestCurrentNoAmbientAgent(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	c := New(Config{
		StorePath:  storePath,
		SocketPath: "",
		Mode:       ModeDurableOnly,
	})

	_, err := c.currentWithInspectors(t.Context(), currentInspectors{
		PID: 1234,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return nil, nil
		},
		ProcessFind:   nil,
		TmuxCurrent:   nil,
		ZellijCurrent: nil,
		HerdrCurrent:  nil,
	})
	if !errors.Is(err, ErrNoCurrentSession) {
		t.Fatalf("expected ErrNoCurrentSession for no ambient agent, got: %v", err)
	}
}

func TestCurrentRejectsUnverifiedPaneCandidates(t *testing.T) {
	t.Parallel()
	location := registry.Location{Kind: registry.MultiplexerZellij, SessionName: "work", PaneID: "terminal_1"}
	caller := processinfo.Process{PID: 200, TTY: "/dev/pts/2"}
	agent := processinfo.Process{PID: 100, TTY: "/dev/pts/1", StartIdentity: "live"}
	opts := currentInspectors{PID: caller.PID, ProcessFind: func(_ context.Context, pid int) (processinfo.Process, bool, error) {
		if pid == caller.PID {
			return caller, true, nil
		}
		return agent, true, nil
	}}
	for _, process := range []*registry.ProcessIdentity{nil, {PID: agent.PID, StartIdentity: "reused"}, {PID: agent.PID, StartIdentity: agent.StartIdentity}} {
		sessions := []registry.Session{{ID: "agent", Location: location, Process: process}}
		_, found, err := resolvePaneSession(t.Context(), sessions, location.Kind, "", location.PaneID, location.SessionName, opts)
		if found || err != nil {
			t.Fatalf("unverified pane resolved: process=%+v found=%v err=%v", process, found, err)
		}
	}
}
