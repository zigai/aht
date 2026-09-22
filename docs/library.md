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
the broker, or `ModeDurableOnly` to use the registry file directly. `StorePath`
and `SocketPath` select another local instance. Saved state may be stale.

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

Codex, Pi, and OMP currently have title readers. Codex resolves names from its
state database and legacy name index. Lookup reads native metadata on demand
and may return an error with partial titles. Cache Pi results when refreshing a
view repeatedly, since Pi records names in its transcript.

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
