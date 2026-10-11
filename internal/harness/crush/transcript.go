package crush

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/transcript"
)

const nativeQuery = `SELECT s.id AS session_id, s.title AS title, '' AS cwd,
 s.created_at AS created, s.updated_at AS updated, m.id AS message_id, m.rowid AS message_order,
 m.role AS role, json_object('parts', m.parts, 'model', m.model) AS body, m.created_at AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id
 WHERE s.parent_session_id IS NULL`

func (crushHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{databaseName}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: transcriptSourceMetadata, Initialize: nil, Extra: nil, Record: nil, FastRecord: nil, Document: nil, Query: transcriptQuery, LocalTitles: false, Parent: nil}
}

func transcriptSources(home string) ([]string, error) {
	path := projectsPath(home)
	if path == "" {
		return nil, nil
	}
	projects, err := readProjects(path)
	if err != nil {
		return nil, err
	}

	return projectDatabases(projects), nil
}

func transcriptSourceMetadata(path string, _ bool, issue func(string, error)) map[string]string {
	projectsFile := projectsPath(harness.HomeDir())
	if projectsFile == "" {
		return nil
	}
	projects, err := readProjects(projectsFile)
	if err != nil {
		issue(projectsFile, err)
		return nil
	}
	database := filepath.Clean(path)
	for _, project := range projects {
		if project.databasePath() == database {
			return map[string]string{"cwd": project.Path}
		}
	}

	return nil
}

func transcriptQuery(context.Context, *sql.DB) (string, transcript.RowReader, error) {
	return nativeQuery, readDatabaseRow, nil
}

func readDatabaseRow(ctx context.Context, t *transcript.Decoder, row transcript.Row) error {
	r, err := transcript.RowRecord(row)
	if err != nil {
		return fmt.Errorf("decode history row: %w", err)
	}
	if t.Conversation.CWD == "" {
		t.Conversation.CWD = t.Metadata["cwd"]
	}
	if t.Conversation.ProjectRoot == "" {
		t.Conversation.ProjectRoot = t.Metadata["cwd"]
	}
	if model := transcript.Str(r, "model"); model != "" {
		t.Conversation.Model = model
	}
	role, ok := transcript.RecognizedRole(row.Role)
	if !ok {
		return nil
	}
	var parts []transcript.Record
	if json.Unmarshal([]byte(transcript.Str(r, "parts")), &parts) != nil {
		return transcript.ErrInvalidRecord
	}
	text, tools := messageText(parts)
	timestamp := transcript.StoredTime(row.Timestamp)
	if text != "" {
		t.Capture(ctx, role, text, row.MessageID, 0, timestamp)
	}
	if tools != "" {
		t.Capture(ctx, "tool", tools, row.MessageID, 0, timestamp)
	}

	return nil
}

func messageText(parts []transcript.Record) (string, string) {
	var text, tools []string
	for _, part := range parts {
		data := transcript.Obj(part, "data")
		switch transcript.Str(part, "type") {
		case "text":
			var hidden bool
			_ = json.Unmarshal(data["hidden"], &hidden)
			if body := transcript.Str(data, "text"); body != "" && !hidden {
				text = append(text, body)
			}
		case "tool_call":
			tools = append(tools, transcript.Str(data, "name")+" "+transcript.Str(data, "input"))
		case "tool_result":
			tools = append(tools, transcript.Str(data, "name")+" "+transcript.Str(data, "content"))
		case "shell_command":
			tools = append(tools, transcript.Str(data, "command")+" "+transcript.Str(data, "output"))
		}
	}

	return strings.Join(text, "\n"), strings.Join(tools, "\n")
}
