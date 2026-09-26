// Package aht is the Go API for AHT: list, watch, and search coding-agent
// sessions, manage harness integrations, and report session state.
//
// Most types here are aliases. Their fields and methods are documented on the
// underlying types, for example [registry.Session], [registry.Filter], and
// [client.Client].
//
// # Connecting
//
// [New] with an empty [Config] connects to the current user's tracker. It uses
// the broker when the tracker runs and falls back to state on disk otherwise.
// Set the Mode field to [ModeRealtimeOnly] to fail when the tracker is not
// running, or [ModeDurableOnly] to read disk only. StorePath and SocketPath
// select another AHT instance. See [client.Config].
//
// Disk reads include writes the tracker has not processed yet. They show what
// was last reported and cannot detect a process that exited since.
//
// # Reading sessions
//
// [client.Client.List] returns sessions matching a [Filter]; empty filter fields match
// anything. [client.Client.Get] looks up a registry ID, [client.Client.Resolve] accepts an ID,
// ID prefix, native session ID, or transcript path, and [client.Client.Current] returns
// the session the calling process runs inside. [client.Client.Summary] counts sessions
// per terminal session.
//
// Read state with [registry.Session.Presence] and [registry.Session.Activity];
// Activity is nil once a session is gone. [registry.Session.Decision] explains
// which source decided the activity. For exhaustive handling, switch on
// Session.Liveness, which is [Live], [Gone], or [Unknown].
//
// The registry holds current state, not history. When a session ends, the
// tracker removes it: at once if AHT knew it only by its process, or after
// the configured tombstone TTL (retention.tombstone_ttl, 10 minutes by
// default) if it has a native session ID or transcript path. Keep your own
// records of ended sessions if you need them, and use [SearchHistory] for past
// conversations. A process AHT finds only by its command name becomes a
// session after it has run for a few seconds, unless a native report or
// catalog entry confirms it sooner.
//
// # Following changes
//
// [client.Client.Watch] calls a function with the current state and after every change
// until the context is canceled. [client.Client.Subscribe] provides the same stream as
// channels. A session the tracker removes is absent from the next snapshot.
// [client.Client.Wait] blocks until one session reaches a presence or
// activity, optionally for a minimum duration; removal of a session it has
// seen satisfies a wait for [PresenceGone].
//
// # Titles and history
//
// [LookupTitles] reads display names from harness files on demand; cache the
// results when refreshing often. [SearchHistory] searches past conversations on
// disk, including ones AHT never tracked, and does not need the tracker. It
// returns partial results with [ErrHistoryIncomplete] when some sources cannot
// be read. Use [HistoryCatalog] to search specific directories.
//
// # Integrations and the tracker
//
// [NewManager] returns a [Manager] that checks, installs, and removes harness
// integrations and controls the background tracker service. [Capabilities]
// reports what each harness supports.
//
// # Reporting state
//
// Integrations normally report through the aht report command. From Go, pass an
// [Observation] to [client.Client.Observe]. Its Evidence is one of [Report], [Sighting],
// [Placement], [Listing], or [Reading]. Set [Reporter] Sequence to have AHT
// reject out-of-order reports.
//
// # Compatibility
//
// The client and the running tracker must be the same AHT version; restart the
// tracker after upgrading. Session JSON has liveness and location objects.
package aht
