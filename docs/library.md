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

## Search history

`SearchHistory` searches retained local conversations, including sessions AHT
never tracked. The tracker, the broker, and the harnesses do not need to be
running. Matching is literal and case-insensitive by default; set
`CaseSensitive: true` to require exact case. Case-insensitive matching applies
Unicode case folding after simple lowercasing, so `STRASSE` matches `Straße`,
ligature sequences match (`ﬃ`/`ffi`), Greek sigma variants match (`Σ`/`ς`), and
Turkish dotted `İ` still matches ASCII `i`. User and assistant messages are
searched by default; system and reasoning text never matches. Set
`IncludeTools: true` to also search tool calls and tool output.

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

	// Explicit sources replace discovery. IndexPath keeps the disposable cache
	// used to skip unchanged transcripts next to the archives it indexes.
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

Search returns partial results when some sources cannot be read, so always
inspect both the result and the error: `errors.Is(err, aht.ErrHistoryIncomplete)`
means at least one source was not searched, while `result.Matches` still holds
everything that was found, `result.Sources` reports each source's coverage
(`searched`, `skipped`, `missing`, `unsupported`, or `failed`), and
`result.Issues` carries bounded diagnostics without transcript text. Invalid
options fail before any history is read with `aht.ErrInvalidHistoryQuery`, and a
canceled context returns the partial result together with
`context.Canceled`.

A `HistoryCatalog` with non-nil `Sources` searches exactly those locations
instead of default discovery; an empty non-nil slice searches nothing. Each
source names a native transcript directory or SQLite database, and repeated
entries are searched once. `HistoryQuery.Harness` restricts matching to one
harness; `HistoryQuery.Dir` restricts it to conversations whose recorded
working directory or workspace root is that path or beneath it.

Search uses a disposable SQLite index so unchanged transcripts are not
rescanned. With an empty `IndexPath` it lives under the user cache directory at
`$XDG_CACHE_HOME/aht/history-v1.sqlite` (mode `0600`) and stores searchable
transcript text: user and assistant messages by default, plus tool calls and
output for histories already searched with `IncludeTools: true`. Deleting it is
safe; the next search rebuilds it. An unusable default cache is renamed aside to
`history-v1.sqlite.invalid` and rebuilt rather than failing the search, and when
the index cannot be used at all the search falls back to scanning histories
directly. A custom `IndexPath` is never replaced: a foreign schema there is
reported as an error and the file is left untouched.
