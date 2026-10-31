package prime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/titlefile"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (primeHarness) SessionTitles(
	ctx context.Context,
	identities []registry.ObservationIdentity,
) ([]string, error) {
	titles := make([]string, len(identities))

	var failures []error

	for index, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}

		if identity.SessionID == "" {
			continue
		}

		if title := strings.TrimSpace(identity.Attributes["prime_session_name"]); title != "" {
			titles[index] = title
			continue
		}

		if identity.SessionPath == "" {
			titles[index] = strings.TrimSpace(identity.Attributes["prime_first_message"])
			continue
		}

		fallback := strings.TrimSpace(identity.Attributes["prime_first_message"])

		title, err := readSessionTitle(ctx, identity.SessionID, identity.SessionPath)
		if title == "" {
			title = fallback
		}

		titles[index] = title

		if err != nil {
			failures = append(failures, fmt.Errorf("read Prime Agent session title: %w", err))
		}
	}

	return titles, errors.Join(failures...)
}

func readSessionTitle(ctx context.Context, sessionID string, path string) (string, error) {
	file, err := titlefile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("open transcript: %w", err)
	}

	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)

	title, valid, err := readSessionHeader(ctx, reader, sessionID)
	if err != nil {
		return "", err
	}

	if !valid {
		return "", nil
	}

	return scanSessionTitle(ctx, reader, title)
}

func readSessionHeader(
	ctx context.Context,
	reader *bufio.Reader,
	sessionID string,
) (string, bool, error) {
	first, err := titlefile.ReadLine(ctx, reader)
	if errors.Is(err, io.EOF) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("read transcript header: %w", err)
	}

	header, err := transcript.ParseTree(first)
	if err != nil {
		return "", false, fmt.Errorf("decode transcript header: %w", err)
	}

	return strings.TrimSpace(header.Title), header.Type == "session" && header.ID == sessionID, nil
}

func scanSessionTitle(ctx context.Context, reader *bufio.Reader, title string) (string, error) {
	firstMessage := ""

	for {
		line, err := titlefile.ReadLine(ctx, reader)
		if errors.Is(err, io.EOF) {
			return firstNonEmpty(title, firstMessage), nil
		}

		if errors.Is(err, titlefile.ErrRecordTooLarge) {
			continue
		}

		if err != nil {
			return title, fmt.Errorf("scan transcript titles: %w", err)
		}

		title = titleFromLine(line, title)
		if firstMessage == "" && bytes.Contains(line, []byte(`"message"`)) {
			firstMessage = firstUserMessage(line)
		}
	}
}

func titleFromLine(line []byte, title string) string {
	if !bytes.Contains(line, []byte(`"session_info"`)) {
		return title
	}

	entry, err := transcript.ParseTree(line)
	if err != nil || entry.Type != "session_info" || strings.TrimSpace(entry.Name) == "" {
		return title
	}

	return strings.TrimSpace(entry.Name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}

	return ""
}

func firstUserMessage(line []byte) string {
	var record transcript.Record
	if json.Unmarshal(line, &record) != nil || transcript.Str(record, "type") != "message" {
		return ""
	}

	message := transcript.Obj(record, "message")
	if transcript.Str(message, "role") != "user" {
		return ""
	}

	var text strings.Builder

	for _, part := range transcript.MessageParts(message["content"], "user", false) {
		if part.Role != "user" {
			continue
		}

		if text.Len() > 0 {
			text.WriteByte(' ')
		}

		text.WriteString(strings.Join(strings.Fields(part.Text), " "))
	}

	return strings.TrimSpace(text.String())
}
