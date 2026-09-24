// Package registry is AHT's session state engine.
//
// Programs that only read the running tracker should use package client or
// package aht instead.
//
// # Model
//
// An [Observation] carries one kind of [Evidence]: a native [Report], a process
// [Sighting], a terminal [Placement], a native catalog [Listing], or a screen
// [Reading]. A [Reducer] built from injected [Rules] folds observations into
// [Session] values. [ActivityAuthority] decides whether hook reports or screen
// readings own a session's activity.
//
// # Storage
//
// State is a snapshot file (schema 3) plus an append-only journal next to it,
// named after the snapshot with a .journal.jsonl suffix. Both belong together
// when moving or backing up an instance.
//
//   - [MemoryStore] is the live state held by the tracker. It applies pending
//     journal entries before each operation and writes coalesced snapshots
//     (25 ms settle, 250 ms maximum) and a final one on shutdown.
//   - [Journal] records writes while no tracker is reachable. Entries are fsynced
//     and the journal is limited to 64 MiB. Without a tracker, a writer folds the
//     snapshot and journal under the store lock and checkpoints the result.
//   - [FileStore] only reads. Reads fold the snapshot and pending journal
//     entries, so they include writes the tracker has not processed yet.
//
// GC and reset are journal commands too, so an unreachable tracker cannot
// restore removed sessions. Snapshots with another schema version are rejected.
package registry
