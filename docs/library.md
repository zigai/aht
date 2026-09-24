# Go library

```sh
go get github.com/zigai/aht
```

Requires Go 1.27.1 or newer. Import `github.com/zigai/aht/pkg/aht` for the client
and session types. See the [API reference](https://pkg.go.dev/github.com/zigai/aht/pkg/aht)
for exported types and methods.

## List sessions

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zigai/aht/pkg/aht"
)

func main() {
	client := aht.New(aht.Config{})
	sessions, err := client.List(context.Background(), aht.Filter{
		Presence: aht.PresenceLive,
		Harness:  aht.HarnessCodex,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, session := range sessions {
		fmt.Printf("%s\t%s\t%s\n", session.ID, session.Harness, session.CWD)
	}
}
```

By default, the client uses the local broker and falls back to saved registry
state when it is unavailable. Set `Config.Mode` to `ModeRealtimeOnly` to require
the broker, or `ModeDurableOnly` to read the snapshot plus pending journal entries.
`StorePath` and `SocketPath` select another local instance. Offline writes are
fsynced to the journal and reduced by the same rules as broker writes. Durable
state reflects accepted observations; it cannot detect a process change by itself.

## Client methods

| Method | Returns or does |
| --- | --- |
| `List(ctx, filter)` | Sessions matching a filter. |
| `Get(ctx, id)` | One session by registry ID. |
| `Resolve(ctx, selector)` | A session by ID, unambiguous prefix, native ID, or path. |
| `Current(ctx)` | The session containing the calling process. |
| `Summary(ctx, filter)` | Counts grouped by terminal session. |
| `Watch(ctx, filter, callback)` | Callbacks with the initial snapshot and later changes. |
| `Subscribe(ctx, filter)` | Snapshot and error channels. Close the subscription when done. |
| `Wait(ctx, options)` | A session once the requested presence or activity is observed. |

## Display titles

`LookupTitles(ctx, sessions)` returns titles in the same order as the input
sessions. It uses each session's native `SessionID` and, where available,
`SessionPath`. An empty title means no name was recorded or AHT has no title
reader for that harness. The `TitleLookup` field returned by `Capabilities`
distinguishes reader support from an unnamed session.

Title support comes from adapter capabilities. Codex resolves names from its
state database and legacy name index; Pi and OMP share transcript metadata
parsing with history. Other adapters may use native databases, indexes, plugin
metadata, or native APIs. Lookup may return an error with partial titles. Cache
results when refreshing a view repeatedly.

## Manage integrations and the tracker

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zigai/aht/pkg/aht"
)

func main() {
	manager := aht.NewManager(aht.ManagerConfig{})
	status, err := manager.IntegrationStatus(context.Background(), aht.HarnessCodex)
	if err != nil {
		log.Fatal(err)
	}
	if status.Status == aht.ArtifactStale {
		fmt.Println("Codex integration needs an update")
	}
}
```

## Search history

`SearchHistory` searches retained local conversations, including sessions AHT
never tracked. The tracker, the broker, and the harnesses do not need to be
running. Matching is literal and case-insensitive by default.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	"github.com/zigai/aht/pkg/aht"
)

func main() {
	ctx := context.Background()

	// Default search: every discovered native history.
	result, err := aht.SearchHistory(ctx, aht.HistoryQuery{Text: "refresh token"})
	report(result, err)

	// Explicit sources.
	catalog := aht.HistoryCatalog{
		Sources: []aht.HistorySource{
			{Harness: aht.HarnessPi, Path: "/archive/pi/sessions"},
			{Harness: aht.HarnessClaude, Path: "/archive/claude/projects"},
		},
		IndexPath: filepath.Join("/archive", "aht-history.sqlite"),
	}
	result, err = catalog.Search(ctx, aht.HistoryQuery{Text: "migration", IncludeTools: true})
	report(result, err)
}

func report(result aht.HistoryResult, err error) {
	if err != nil && !errors.Is(err, aht.ErrHistoryIncomplete) {
		log.Fatal(err)
	}
	for _, match := range result.Matches {
		conversation := match.Conversation
		fmt.Printf("%s\t%s\t%s\n", conversation.Harness, conversation.SessionID, conversation.Title)
	}
	if errors.Is(err, aht.ErrHistoryIncomplete) {
		fmt.Printf("partial results: %d source issues\n", len(result.Issues))
	}
}
```

Search returns partial results when some sources cannot be read.

## Session and observation model

Harness names are strings. Public name constants live in `pkg/aht`; the registry
accepts names and activity policies through injected `registry.Rules`.

A session has `Liveness` of type `Live`, `Gone`, or `Unknown`. Read
`session.Presence()`, `session.Activity()`, and `session.Decision()` for rendering
and filtering. Gone sessions return nil activity. Unknown presence can retain a
known activity. `IdentityState` distinguishes process-only provisional sessions
from sessions with native identity. `Incarnation` records the current process and
sticky native-end state.

`Location` contains the multiplexer kind, server, session, and pane identifiers.
The separate `Tmux` field and `TmuxContext` type are removed. Use
`Filter.MultiplexerSession` for terminal-session filtering.

An observation contains `Harness`, `At`, `Subject`, and sealed `Evidence`:

| Evidence | Meaning |
| --- | --- |
| `*Report` | Native lifecycle/activity, typed reporter identity, optional metadata. |
| `*Sighting` | Process identity and presence. |
| `*Placement` | Process identity and terminal location. |
| `*Listing` | Native catalog metadata and resume command. |
| `*Reading` | Screen activity and detection rule evidence. |

`Reporter` owns `Integration`, `Version`, `Sequence`, and `MultiSession`.
Use `Client.Observe` or `ObserveBatch` to submit observations. Low-level callers
construct `registry.NewReducer(rules)`; `Apply` returns state and consumer-visible
changes. `registry.NewJournal(path, rules)` provides durable mutations.
`FileStore` only reads persisted state; it has no observation-write methods.
The broker holds the live-owner lock, coalesces snapshots, and flushes on close.
GC and reset are journal commands, so an unreachable broker cannot resurrect
removed rows.

Snapshots use schema 3; other schema versions are rejected. Broker protocol 2
requires matching client and server versions; filters use snake_case JSON fields. Restart the tracker
when upgrading. Session JSON uses `liveness` and `location`; observation JSON
has `kind` and a corresponding `evidence` object.
