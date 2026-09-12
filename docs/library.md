# Go Library Guide

AHT exposes public Go packages for integrating agent tracking, realtime IPC, multiplexer discovery, registry storage, and harness management into Go tools and applications.

## Packages Overview

| Package | Import Path | Description |
|---|---|---|
| [`aht`](https://pkg.go.dev/github.com/zigai/aht/pkg/aht) | `github.com/zigai/aht/pkg/aht` | **Canonical entrypoint**: primary client and domain models in a single, clean import. |
| [`broker`](https://pkg.go.dev/github.com/zigai/aht/pkg/broker) | `github.com/zigai/aht/pkg/broker` | Unix domain socket client and protocol for direct realtime broker IPC. |
| [`tmux`](https://pkg.go.dev/github.com/zigai/aht/pkg/tmux) | `github.com/zigai/aht/pkg/tmux` | Inspect and discover tmux environment, current pane, and session topology. |
| [`client`](https://pkg.go.dev/github.com/zigai/aht/pkg/client) | `github.com/zigai/aht/pkg/client` | High-level client package supporting realtime, durable, or auto-fallback modes. |
| [`registry`](https://pkg.go.dev/github.com/zigai/aht/pkg/registry) | `github.com/zigai/aht/pkg/registry` | Core domain storage engines (`FileStore`, `MemoryStore`) and interfaces. |
| [`zellij`](https://pkg.go.dev/github.com/zigai/aht/pkg/zellij) | `github.com/zigai/aht/pkg/zellij` | Inspect and discover Zellij sessions, panes, and screen snapshots. |
| [`mux`](https://pkg.go.dev/github.com/zigai/aht/pkg/mux) | `github.com/zigai/aht/pkg/mux` | Common polymorphic types and helpers for terminal multiplexers. |
| [`manage`](https://pkg.go.dev/github.com/zigai/aht/pkg/manage) | `github.com/zigai/aht/pkg/manage` | Programmatic hook installation, removal, and background tracker daemon service control. |
| [`harness`](https://pkg.go.dev/github.com/zigai/aht/pkg/harness) | `github.com/zigai/aht/pkg/harness` | Supported harness catalog, alias normalization, and process-to-harness command matching. |

---

## 1. `pkg/aht` (Canonical API)

The `aht` package is the recommended entrypoint for Go applications. It bundles the primary client, operating modes, and all core domain types into a **single import** so you never have to juggle multiple packages.

### Querying Live Sessions

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zigai/aht/pkg/aht"
)

func main() {
	ctx := context.Background()

	// Connect to AHT (single import, no "client" name collision)
	client := aht.New(aht.Config{
		Mode: aht.ModeRealtimeOnly, // or aht.ModeAuto
	})

	// Query live sessions
	sessions, err := client.List(ctx, aht.Filter{
		Presence: aht.PresenceLive,
		Harness:  aht.HarnessClaude,
	})
	if err != nil {
		log.Fatalf("list sessions failed: %v", err)
	}

	for _, s := range sessions {
		activity := "unknown"
		if s.Activity != nil {
			activity = string(*s.Activity)
		}
		fmt.Printf("[%s] %s (%s) — Activity: %s\n",
			s.Presence, s.Harness, s.SessionID, activity)
	}
}
```

### Channel Streaming

Using the client and context above, subscribe to all live harnesses. Both `Subscribe` and the client's callback-based `Watch` require a running broker, including in auto mode. Close a subscription or cancel its context when finished.

```go
sub, err := client.Subscribe(ctx, aht.Filter{Presence: aht.PresenceLive})
if err != nil {
	log.Fatal(err)
}
defer sub.Close()

for snapshot := range sub.Snapshots {
	fmt.Printf("Revision %d: %d live agents\n", snapshot.Revision, len(snapshot.Sessions))
}
for err := range sub.Errors {
	log.Printf("subscription ended: %v", err)
}
```

---

## 2. `pkg/client`

The `client` package provides a unified API supporting three operating modes:

- `client.ModeAuto` (default): queries the broker socket first; falls back to reading `sessions.json` on disk if the broker daemon is offline.
- `client.ModeRealtimeOnly`: strictly dials the broker socket; returns `client.ErrUnavailable` immediately if the broker is stopped (never touches disk or takes file locks).
- `client.ModeDurableOnly`: reads and writes directly to the durable registry file, bypassing the broker daemon.

### Error classification

Use `errors.Is(err, registry.ErrSessionNotFound)` for a missing session and
`errors.Is(err, registry.ErrObservationConflict)` for an observation rejected
because it conflicts with accepted evidence. These classifications are stable in
durable, realtime, and auto modes, including when the same auto client switches
to durable storage after the broker stops. A rejected observation leaves the
accepted session unchanged.

Broker operation failures also support `errors.As` (or `errors.AsType`) to
`*client.OperationError`, retaining the broker's `Code` and `Message`.
Its error chain includes `*broker.RemoteError`, which unwraps wire code
`not_found` to `registry.ErrSessionNotFound` and `observation_conflict` to
`registry.ErrObservationConflict`. Direct `pkg/broker` callers can inspect the
same classifications. Durable failures need not contain either broker error
type.

Other remote codes remain operation errors without an invented registry
classification. In particular, the remote `canceled` code does not distinguish
context cancellation from deadline expiry and does not unwrap to either context
sentinel. Local context errors retain their existing classification.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zigai/aht/pkg/client"
)

func main() {
	ctx := context.Background()

	aht := client.New(client.Config{
		Mode: client.ModeRealtimeOnly,
	})

	sessions, err := aht.List(ctx, client.Filter{Presence: client.PresenceLive})
	if err != nil {
		log.Fatalf("failed to list sessions: %v", err)
	}

	for _, session := range sessions {
		fmt.Printf("[%s] %s (%s)\n", session.Presence, session.Harness, session.SessionID)
	}
}
```

---

## 3. `pkg/broker`

The `broker` package speaks line-delimited JSON directly to AHT's Unix domain socket. It imports `pkg/registry`, including that package's transitive dependencies.

Use `broker` when you are building an integration (like an editor extension, status line, or companion daemon) that requires:

- Direct access to the broker protocol without the higher-level client's mode selection.
- Immediate `ErrUnavailable` failures when AHT is stopped, with zero filesystem locking or disk fallback.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

func main() {
	ctx := context.Background()
	client := broker.NewClientForSocket(broker.DefaultSocketPath())

	// Stream live snapshots over the Unix socket
	sub, err := client.Subscribe(ctx, registry.Filter{Presence: registry.PresenceLive})
	if err != nil {
		if broker.IsUnavailable(err) {
			log.Println("AHT broker is not running")
			return
		}
		log.Fatalf("subscribe failed: %v", err)
	}
	defer sub.Close()

	for snapshot := range sub.Snapshots {
		for _, s := range snapshot.Sessions {
			fmt.Printf("[%s] %s in pane %s\n", s.Harness, s.SessionID, s.Tmux.PaneID)
		}
	}
	for err := range sub.Errors {
		log.Printf("subscription ended: %v", err)
	}
}
```

---

## 4. `pkg/tmux`

The `tmux` package discovers tmux environment variables and pane metadata for terminal integrations.

```go
package main

import (
	"context"
	"fmt"

	"github.com/zigai/aht/pkg/tmux"
)

func main() {
	ctx := context.Background()

	// Discover the current tmux pane, window, and server socket
	current, err := tmux.Current(ctx)
	if err != nil {
		fmt.Println("Failed to inspect current tmux pane:", err)
		return
	}

	fmt.Printf("Tmux Server: %s, Session: %s, Window: %s, Pane: %s\n",
		current.ServerSocket, current.SessionName, current.WindowName, current.PaneID)

	// List all panes across the server
	panes, err := tmux.ListPanes(ctx)
	if err != nil {
		fmt.Println("Failed to list panes:", err)
		return
	}
	fmt.Printf("Total active panes: %d\n", len(panes))
}
```

---

## 5. `pkg/registry`

The `registry` package defines the core domain model and storage backends.

### Storage Engines

- `FileStore`: Thread-safe, durable file storage backed by JSON and file locking.
- `MemoryStore`: Loads a durable snapshot once, then keeps observations in memory. Call `Flush` or run `RunPersistence` to persist subsequent changes. Tests can use a snapshot path under `t.TempDir()` without starting persistence.

### In-Memory Unit Testing Example

```go
package myapp_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestAgentTracking(t *testing.T) {
	ctx := t.Context()
	store, err := registry.OpenMemoryStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}

	presence := registry.PresenceLive
	activity := registry.ActivityRunning

	// Record an observation
	session, err := store.Observe(ctx, registry.Observation{
		Harness:  registry.HarnessCodex,
		Source:   registry.ObservationSourceNative,
		Evidence: registry.ObservationEvidenceNativeEvent,
		Identity: registry.ObservationIdentity{
			SessionID: "test-session-1",
		},
		Catalog:    &registry.CatalogMetadata{CWD: "/workspace/repo"},
		Presence:   &presence,
		Activity:   &activity,
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("observe failed: %v", err)
	}

	if session.Presence != registry.PresenceLive {
		t.Errorf("expected presence live, got %s", session.Presence)
	}
}
```

---

## 6. `pkg/manage`

The `manage` package allows Go applications to programmatically install integrations and control the background tracker service.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zigai/aht/pkg/manage"
	"github.com/zigai/aht/pkg/registry"
)

func main() {
	ctx := context.Background()
	mgr := manage.New(manage.Config{})

	// 1. Install Claude Code hooks
	result, err := mgr.InstallIntegration(ctx, registry.HarnessClaude, manage.IntegrationOptions{
		Force: true,
	})
	if err != nil {
		log.Fatalf("failed to install integration: %v", err)
	}
	fmt.Printf("Installed %s integration at %s (changed: %t)\n", result.Harness, result.Path, result.Changed)

	// 2. Inspect integration status
	status, err := mgr.IntegrationStatus(ctx, registry.HarnessClaude)
	if err != nil {
		log.Fatalf("failed to inspect status: %v", err)
	}
	fmt.Printf("Claude integration status: %s (%s)\n", status.Status, status.Message)

	// 3. Inspect the background tracker daemon (does not start it)
	serviceStatus, err := mgr.TrackerStatus(ctx)
	if err != nil {
		log.Fatalf("failed to get tracker status: %v", err)
	}
	fmt.Printf("Tracker running: %t, installed: %t\n", serviceStatus.Running, serviceStatus.Installed)
}
```

---

## 7. `pkg/harness`

The `harness` package provides discovery, alias normalization, and process matching for all supported coding agents.

```go
package main

import (
	"fmt"

	"github.com/zigai/aht/pkg/harness"
)

func main() {
	// Parse user input or CLI args into canonical harness IDs
	id, err := harness.Parse("kimi-code")
	if err == nil {
		fmt.Printf("Parsed harness: %s\n", id) // "kimi-code"
	}

	// Identify harness from process executable path
	if h, ok := harness.FromCommand("/usr/local/bin/codex"); ok {
		fmt.Printf("Identified harness from command: %s\n", h) // "codex"
	}

	// List all supported harnesses
	for _, supported := range harness.Supported() {
		fmt.Printf("Supported harness: %s\n", supported)
	}
}
```
