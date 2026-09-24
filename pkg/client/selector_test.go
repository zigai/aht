package client_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/internal/brokerserver"
	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

func TestSelectorExactIDVsPrefixPrecedence(t *testing.T) {
	t.Parallel()

	sessions := []registry.Session{
		testSessionWithID("claude-1000", "claude", "sess-1"),
		testSessionWithID("claude-1000-worker", "claude", "sess-2"),
	}

	// Exact match must win over prefix match
	resolved, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "claude-1000",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("expected exact match, got error: %v", err)
	}
	if resolved.ID != "claude-1000" {
		t.Fatalf("resolved ID = %q, want claude-1000", resolved.ID)
	}

	// Ambiguous prefix matching both sessions must return ErrAmbiguousSession
	_, err = client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "claude-10",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if !errors.Is(err, client.ErrAmbiguousSession) {
		t.Fatalf("expected ErrAmbiguousSession, got: %v", err)
	}
}

func TestSelectorNativeIDsAndPaths(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	sessPath := filepath.Join(tempDir, "agent-session.json")
	sessions := []registry.Session{
		testSessionWithNative("codex-0001", "codex", "native-token-123", ""),
		testSessionWithNative("cursor-0002", "cursor", "native-token-456", sessPath),
	}

	// Match by native session ID
	resolved, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "native-token-123",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("resolve by native ID failed: %v", err)
	}
	if resolved.ID != "codex-0001" {
		t.Fatalf("resolved ID = %q, want codex-0001", resolved.ID)
	}

	// Match by session path with normalization
	sep := string(filepath.Separator)
	dirtySessionPath := tempDir + sep + "." + sep + "agent-session.json"
	if dirtySessionPath == sessions[1].SessionPath {
		t.Fatalf("dirtySessionPath %q was unexpectedly equal to stored %q", dirtySessionPath, sessions[1].SessionPath)
	}
	resolved, err = client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         dirtySessionPath,
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("resolve by normalized path failed: %v", err)
	}
	if resolved.ID != "cursor-0002" {
		t.Fatalf("resolved ID = %q, want cursor-0002", resolved.ID)
	}
}

func TestSelectorNoMatchAndAmbiguity(t *testing.T) {
	t.Parallel()

	sessions := []registry.Session{
		testSessionWithID("session-a", "claude", "id-a"),
		testSessionWithID("session-b", "claude", "id-b"),
	}

	// No match returns ErrSessionNotFound
	_, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "nonexistent",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if !errors.Is(err, registry.ErrSessionNotFound) || !errors.Is(err, client.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}

	// Ambiguity with multiple prefix matches
	_, err = client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "session",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if !errors.Is(err, client.ErrAmbiguousSession) {
		t.Fatalf("expected ErrAmbiguousSession, got: %v", err)
	}
}

func TestSelectorTwoServersWithSamePane(t *testing.T) {
	t.Parallel()

	sessions := []registry.Session{
		testSessionWithMultiplexer("s1", registry.MultiplexerTmux, "/tmp/tmux-1.sock", "%0"),
		testSessionWithMultiplexer("s2", registry.MultiplexerTmux, "/tmp/tmux-2.sock", "%0"),
	}

	// Bare %0 must be rejected as ambiguous across two servers
	_, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "%0",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if !errors.Is(err, client.ErrAmbiguousSession) {
		t.Fatalf("expected ErrAmbiguousSession for bare %%0 across servers, got: %v", err)
	}

	// Server-qualified pane %0 must resolve uniquely
	resolved, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "/tmp/tmux-1.sock",
		MultiplexerPane:   "%0",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("expected unique resolution with server qualifier, got: %v", err)
	}
	if resolved.ID != "s1" {
		t.Fatalf("resolved ID = %q, want s1", resolved.ID)
	}
}

func TestSelectorCrossMultiplexerPanes(t *testing.T) {
	t.Parallel()

	sessions := []registry.Session{
		testSessionWithMultiplexer("tmux-sess", registry.MultiplexerTmux, "/tmp/tmux.sock", "%0"),
		testSessionWithMultiplexer("tmux-other", registry.MultiplexerTmux, "/tmp/tmux.sock", "%1"),
		testSessionWithMultiplexer("zellij-sess", registry.MultiplexerZellij, "", "%0"),
	}

	// Bare %0 is ambiguous across multiplexer kinds
	_, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "%0",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if !errors.Is(err, client.ErrAmbiguousSession) {
		t.Fatalf("expected ErrAmbiguousSession across multiplexers, got: %v", err)
	}

	// Kind-qualified pane %0 resolves uniquely
	resolved, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   client.MultiplexerTmux,
		MultiplexerServer: "",
		MultiplexerPane:   "%0",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("expected unique resolution for tmux, got: %v", err)
	}
	if resolved.ID != "tmux-sess" {
		t.Fatalf("resolved ID = %q, want tmux-sess", resolved.ID)
	}

	resolved, err = client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   client.MultiplexerZellij,
		MultiplexerServer: "",
		MultiplexerPane:   "%0",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("expected unique resolution for zellij, got: %v", err)
	}
	if resolved.ID != "zellij-sess" {
		t.Fatalf("resolved ID = %q, want zellij-sess", resolved.ID)
	}
}

func TestSelectorPathNormalizationAndSubtree(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	projRoot := filepath.Join(tempDir, "myproject")
	subDir := filepath.Join(projRoot, "src", "pkg")

	session := testSessionWithPaths("path-sess", projRoot, subDir)
	sessions := []registry.Session{session}

	// Exact project root match with dirty path
	sep := string(filepath.Separator)
	dirtyProj := projRoot + sep + "." + sep + "sub" + sep + ".."
	if dirtyProj == session.ProjectRoot {
		t.Fatalf("dirtyProj %q was unexpectedly equal to stored %q", dirtyProj, session.ProjectRoot)
	}
	resolved, err := client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           dirtyProj,
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("exact project match failed: %v", err)
	}
	if resolved.ID != "path-sess" {
		t.Fatalf("resolved ID = %q, want path-sess", resolved.ID)
	}

	// Non-matching exact project
	_, err = client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           tempDir,
		ProjectSubtree:    false,
		CWD:               "",
	})
	if !errors.Is(err, registry.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for non-matching exact project, got: %v", err)
	}

	// Subtree matching with parent directory matches
	resolved, err = client.ResolveSessions(sessions, client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           tempDir,
		ProjectSubtree:    true,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("subtree match failed: %v", err)
	}
	if resolved.ID != "path-sess" {
		t.Fatalf("resolved ID = %q, want path-sess", resolved.ID)
	}
}

func TestClientResolveDurableMode(t *testing.T) {
	t.Parallel()

	storePath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(storePath)
		_ = os.Remove(broker.SocketPath(storePath))
	})

	store := registry.NewJournal(storePath, catalog.Rules{})
	createdSession := setupTestSessionForStore(t, store)

	durableClient := client.New(client.Config{
		StorePath:  storePath,
		SocketPath: "",
		Mode:       client.ModeDurableOnly,
	})

	resolved, err := durableClient.Resolve(t.Context(), client.Selector{
		ID:                "",
		Reference:         "sess-durable-1",
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		t.Fatalf("durable resolve failed: %v", err)
	}
	if resolved.ID != createdSession.ID {
		t.Fatalf("durable resolved ID = %q, want %q", resolved.ID, createdSession.ID)
	}
}

func TestClientResolveRealtimeBrokerMode(t *testing.T) {
	t.Parallel()

	storePath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(storePath)
		_ = os.Remove(broker.SocketPath(storePath))
	})

	memoryStore, err := registry.OpenMemoryStore(storePath, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	createdSession := setupTestSessionForStore(t, memoryStore)

	socketPath := broker.SocketPath(storePath)
	ready := make(chan struct{})
	serverErrors := make(chan error, 1)
	server := brokerserver.New(brokerserver.Options{
		Store:      memoryStore,
		SocketPath: socketPath,
		Ready:      ready,
	})

	serverCtx, cancelServer := context.WithCancel(t.Context())
	defer cancelServer()
	go func() {
		serverErrors <- server.Serve(serverCtx)
	}()
	select {
	case <-ready:
	case err := <-serverErrors:
		t.Fatalf("broker exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not become ready")
	}

	realtimeClient := client.New(client.Config{
		StorePath:  storePath,
		SocketPath: socketPath,
		Mode:       client.ModeRealtimeOnly,
	})
	if err := realtimeClient.Ping(t.Context()); err != nil {
		t.Fatalf("realtime ping failed: %v", err)
	}

	resolved, err := realtimeClient.Resolve(t.Context(), client.Selector{
		ID:                "",
		Reference:         "",
		Harness:           client.Harness("claude"),
		MultiplexerKind:   client.MultiplexerTmux,
		MultiplexerServer: "/tmp/tmux-main.sock",
		MultiplexerPane:   "%42",
		Project:           "/tmp/project",
		ProjectSubtree:    false,
		CWD:               "/tmp/project",
	})
	if err != nil {
		t.Fatalf("realtime resolve failed: %v", err)
	}
	if resolved.ID != createdSession.ID {
		t.Fatalf("realtime resolved ID = %q, want %q", resolved.ID, createdSession.ID)
	}

	cancelServer()
	<-serverErrors
}

func setupTestSessionForStore(t *testing.T, store registry.Store) registry.Session {
	t.Helper()

	live := registry.PresenceLive
	activity := registry.ActivityRunning
	obs := registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-durable-1"}, Evidence: &registry.Report{Lifecycle: nil, Claim: &live, Activity: &activity, Process: nil, Location: &registry.Location{
		Kind:            registry.MultiplexerTmux,
		ServerID:        "/tmp/tmux-main.sock",
		SessionID:       "$0",
		SessionName:     "main",
		WorkspaceID:     "",
		WorkspaceName:   "",
		TabID:           "",
		TabIndex:        "",
		TabName:         "",
		WindowID:        "@0",
		WindowIndex:     "0",
		WindowName:      "win",
		PaneID:          "%42",
		PaneIndex:       "0",
		PaneCurrentPath: "/tmp/project",
		PanePID:         0,
		PaneTTY:         "",
		ClientTTY:       "",
	}, Listing: &registry.Listing{
		ResumeCommand: nil,
		CWD:           "/tmp/project",
		ProjectRoot:   "/tmp/project",
		ProcessPID:    0,
		Current:       false,
	}, Attributes: nil, Payload: nil}}

	session, err := store.Observe(t.Context(), obs)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func testSessionWithID(id, harness, sessionID string) registry.Session {
	return registry.Session{
		SchemaVersion: 2,
		ID:            id,
		Harness:       registry.Harness(harness),
		SessionID:     sessionID,
		SessionPath:   "",
		ResumeCommand: nil,
		CWD:           "",
		ProjectRoot:   "",
		Process:       nil,
		Location:      registry.Location{},

		Observations:      registry.Observations{},
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		PresenceChangedAt: time.Now().UTC(),
		ActivityChangedAt: time.Now().UTC(),
		Liveness:          registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
	}
}

func testSessionWithNative(id, harness, sessionID, sessionPath string) registry.Session {
	s := testSessionWithID(id, harness, sessionID)
	s.SessionPath = sessionPath
	return s
}

func testSessionWithMultiplexer(id string, kind registry.MultiplexerKind, serverID, paneID string) registry.Session {
	s := testSessionWithID(id, "claude", "sess-"+id)
	s.Location = registry.Location{
		Kind:            kind,
		ServerID:        serverID,
		SessionID:       "",
		SessionName:     "",
		WorkspaceID:     "",
		WorkspaceName:   "",
		TabID:           "",
		TabIndex:        "",
		TabName:         "",
		WindowID:        "",
		WindowIndex:     "",
		WindowName:      "",
		PaneID:          paneID,
		PaneIndex:       "",
		PaneCurrentPath: "",
		PanePID:         0,
		PaneTTY:         "",
		ClientTTY:       "",
	}
	return s
}

func testSessionWithPaths(id, projectRoot, cwd string) registry.Session {
	s := testSessionWithID(id, "claude", "sess-"+id)
	s.ProjectRoot = projectRoot
	s.CWD = cwd
	return s
}

func TestResolveWithSessionLister(t *testing.T) {
	t.Parallel()

	store, err := registry.OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	live := registry.PresenceLive
	session, err := store.Observe(t.Context(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-custom-store"}, Evidence: &registry.Report{Claim: &live}})
	if err != nil {
		t.Fatal(err)
	}

	// Test with a direct SessionLister (MemoryStore), bypassing *Client
	var lister client.SessionLister = store
	resolved, err := client.Resolve(t.Context(), lister, client.Selector{
		Reference: session.ID,
	})
	if err != nil {
		t.Fatalf("Resolve with SessionLister failed: %v", err)
	}
	if resolved.ID != session.ID {
		t.Fatalf("Resolve ID = %q, want %q", resolved.ID, session.ID)
	}

	// Test nil lister
	_, err = client.Resolve(t.Context(), nil, client.Selector{Reference: session.ID})
	if err == nil {
		t.Fatal("expected error with nil SessionLister")
	}
}
