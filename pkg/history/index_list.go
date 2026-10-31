package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (index *historyIndex) prefetchConversations(ctx context.Context, filter Filter) (err error) {
	predicate, args := index.selectionSQL(filter)

	parents, err := json.Marshal(index.listParents())
	if err != nil {
		return fmt.Errorf("encode indexed parents: %w", err)
	}

	query := "SELECT c.id,c.file_id,c.metadata FROM conversations c JOIN " +
		"files f ON f.id=c.file_id WHERE (" +
		predicate +
		") OR c.file_id IN (SELECT value FROM json_each(?)) ORDER BY " +

		"c.id"

	args = append(args, string(parents))

	rows, err := index.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return index.failure(fmt.Errorf("prefetch indexed conversations: %w", err))
	}

	defer func() { err = errors.Join(err, rows.Close()) }()

	index.listed = map[int64][]indexedConversation{}

	for rows.Next() {
		var (
			conversation indexedConversation
			fileID       int64
			metadata     string
		)
		if err := rows.Scan(&conversation.id, &fileID, &metadata); err != nil {
			return fmt.Errorf("read listed conversation: %w", err)
		}

		if err := json.Unmarshal([]byte(metadata), &conversation.conversation); err != nil {
			return fmt.Errorf("decode listed conversation: %w", err)
		}

		index.listed[fileID] = append(index.listed[fileID], conversation)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan listed conversations: %w", err)
	}

	return nil
}

func (index *historyIndex) listParents() []int64 {
	parents := map[int64]bool{}

	for _, file := range index.files {
		harnessID := registry.Harness(file.harness)

		parent := catalog.TranscriptFor(harnessID).Parent
		if parent == nil {
			continue
		}

		path := parent(file.path)
		if path == "" {
			continue
		}

		if owner, ok := index.files[fileKey(Source{Harness: harnessID, Path: ""}, path)]; ok {
			parents[owner.id] = true
		}
	}

	ids := make([]int64, 0, len(parents))
	for id := range parents {
		ids = append(ids, id)
	}

	return ids
}
