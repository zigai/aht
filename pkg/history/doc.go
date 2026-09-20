// Package history searches retained local coding-agent conversations independently
// of AHT's live registry. Readers never launch a harness or change its history.
// A disposable local SQLite index caches searchable text and refreshes changed histories.
// Search includes user and assistant text by default; tool content is opt-in.
package history
