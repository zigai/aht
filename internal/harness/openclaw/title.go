package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/zigai/aht/pkg/registry"
)

const openclawTitleOutputLimit = 1 << 20

var (
	errOpenClawTitleOutputTooLarge = errors.New("OpenClaw Gateway response exceeded the title lookup limit")
	errOpenClawGatewayCallFailed   = errors.New("OpenClaw Gateway call failed")
)

type openclawTitleBuffer struct {
	bytes.Buffer

	exceeded bool
}

type openclawResolvedSession struct {
	OK      bool   `json:"ok"`
	Key     string `json:"key"`
	AgentID string
}

type openclawDescribedSession struct {
	Session *openclawSessionTitle `json:"session"`
}

type openclawSessionTitle struct {
	SessionID    string
	Label        string
	DisplayName  string
	DerivedTitle string
	AutoLabel    string
}

func (buffer *openclawTitleBuffer) Write(data []byte) (int, error) {
	if len(data) > openclawTitleOutputLimit-buffer.Len() {
		buffer.exceeded = true
		return 0, errOpenClawTitleOutputTooLarge
	}
	n, err := buffer.Buffer.Write(data)
	if err != nil {
		return n, fmt.Errorf("buffer OpenClaw Gateway response: %w", err)
	}
	return n, nil
}

func (session openclawSessionTitle) title() string {
	for _, title := range []string{session.Label, session.DisplayName, session.DerivedTitle, session.AutoLabel} {
		if title = strings.TrimSpace(title); title != "" {
			return title
		}
	}
	return ""
}

func (openclawHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup OpenClaw session titles: %w", err)
	}
	if len(identities) == 0 {
		return titles, nil
	}
	binary, err := exec.LookPath(openclawCommand)
	if err != nil {
		return titles, fmt.Errorf("find OpenClaw CLI: %w", err)
	}
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, fmt.Errorf("lookup OpenClaw session titles: %w", err))...)
		}
		if identity.SessionID == "" {
			continue
		}
		title, err := lookupOpenClawSessionTitle(ctx, binary, identity)
		if err != nil {
			failures = append(failures, fmt.Errorf("lookup OpenClaw title for session %q: %w", identity.SessionID, err))
			continue
		}
		titles[i] = title
	}
	return titles, errors.Join(failures...)
}

func lookupOpenClawSessionTitle(ctx context.Context, binary string, identity registry.ObservationIdentity) (string, error) {
	sessionID := identity.SessionID
	resolved, err := resolveOpenClawSession(ctx, binary, identity)
	if err != nil {
		return "", err
	}
	if !resolved.OK || resolved.Key == "" {
		return "", nil
	}
	var described openclawDescribedSession
	if err := callOpenClawGateway(ctx, binary, "sessions.describe", map[string]any{
		"key":                  resolved.Key,
		"agentId":              resolved.AgentID,
		"includeDerivedTitles": true,
	}, &described); err != nil {
		return "", err
	}
	if described.Session == nil {
		return "", nil
	}
	if described.Session.SessionID != sessionID && strings.TrimSpace(identity.Attributes["openclaw_session_key"]) != sessionID {
		return "", nil
	}
	return described.Session.title(), nil
}

func resolveOpenClawSession(ctx context.Context, binary string, identity registry.ObservationIdentity) (openclawResolvedSession, error) {
	sessionID := identity.SessionID
	agentID := strings.TrimSpace(identity.Attributes["openclaw_agent_id"])
	resolveByID := map[string]any{
		"sessionId":    sessionID,
		"allowMissing": true,
	}
	if agentID != "" {
		resolveByID["agentId"] = agentID
	}
	var resolved openclawResolvedSession
	if err := callOpenClawGateway(ctx, binary, "sessions.resolve", resolveByID, &resolved); err != nil {
		return resolved, err
	}
	if !resolved.OK {
		key := strings.TrimSpace(identity.Attributes["openclaw_session_key"])
		if key == "" {
			key = sessionID
		}
		resolveByKey := map[string]any{
			"key":          key,
			"allowMissing": true,
		}
		if agentID != "" {
			resolveByKey["agentId"] = agentID
		}
		if err := callOpenClawGateway(ctx, binary, "sessions.resolve", resolveByKey, &resolved); err != nil {
			return resolved, err
		}
	}
	return resolved, nil
}

func callOpenClawGateway(ctx context.Context, binary, method string, params any, result any) error {
	encodedParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s parameters: %w", method, err)
	}
	command := exec.CommandContext(ctx, binary, "gateway", "call", method, "--params", string(encodedParams), "--timeout", "5000", "--json")
	var stdout openclawTitleBuffer
	var stderr openclawTitleBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("call OpenClaw Gateway %s: %w", method, ctx.Err())
		}
		if stdout.exceeded || stderr.exceeded {
			return fmt.Errorf("OpenClaw Gateway %s: %w", method, errOpenClawTitleOutputTooLarge)
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message != "" {
			return fmt.Errorf("OpenClaw Gateway %s failed: %s: %w", method, message, errOpenClawGatewayCallFailed)
		}
		return fmt.Errorf("call OpenClaw Gateway %s: %w", method, err)
	}
	response := stdout.Bytes()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response, &envelope); err != nil {
		return fmt.Errorf("decode OpenClaw Gateway %s response: %w", method, err)
	}
	if payload, ok := envelope["payload"]; ok {
		response = payload
	}
	if err := json.Unmarshal(response, result); err != nil {
		return fmt.Errorf("decode OpenClaw Gateway %s result: %w", method, err)
	}
	return nil
}
