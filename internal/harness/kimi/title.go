package kimi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/internal/harness/titlefile"
	"github.com/zigai/aht/pkg/registry"
)

const (
	maxKimiMetadataBytes        = 1 << 20
	maxKimiACPLineBytes         = 16 << 20
	kimiACPRequestTimeout       = 10 * time.Second
	kimiACPReaderBufferBytes    = 64 << 10
	kimiACPInitializeRequestID  = 1
	kimiACPSessionListRequestID = 2
)

var (
	errKimiMetadataTooLarge                 = errors.New("kimi code workspace metadata exceeds 1 MiB")
	errKimiACPRequestFailed                 = errors.New("kimi ACP request failed")
	errKimiACPResponseMissingResult         = errors.New("kimi ACP response has no result")
	errKimiACPMessageTooLarge               = errors.New("kimi ACP message exceeds 16 MiB")
	errKimiSessionMatchesMultipleWorkspaces = errors.New("kimi code session matched multiple workspaces")
)

type kimiWorkDir struct {
	Path string `json:"path"`
}

type kimiMetadata struct {
	WorkDirs []kimiWorkDir `json:"work_dirs"`
}

type kimiACPSessionInfo struct {
	SessionID string
	Title     string
}

type kimiACPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type kimiACPMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *kimiACPError   `json:"error"`
}

type kimiTitleGroup struct {
	indicesByID map[string][]int
}

type kimiSessionTitleFallback struct {
	indicesByID map[string][]int
	indices     map[int]bool
}

func (kimiCodeHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Kimi Code session titles: %w", err)
	}
	if len(identities) == 0 {
		return titles, nil
	}
	groups, fallback, err := kimiSessionTitleGroups(identities)
	if err != nil {
		return titles, err
	}
	failures := loadKimiFallbackGroups(ctx, groups, fallback)
	return lookupKimiSessionGroups(ctx, groups, fallback, titles, failures)
}

func loadKimiFallbackGroups(ctx context.Context, groups map[string]*kimiTitleGroup, fallback kimiSessionTitleFallback) []error {
	if len(fallback.indicesByID) == 0 {
		return nil
	}
	metadata, err := readKimiMetadata(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{err}
	}
	addKimiFallbackGroups(groups, fallback, metadata)
	return nil
}

func lookupKimiSessionGroups(
	ctx context.Context,
	groups map[string]*kimiTitleGroup,
	fallback kimiSessionTitleFallback,
	titles []string,
	failures []error,
) ([]string, error) {
	if len(groups) == 0 {
		return titles, errors.Join(failures...)
	}
	binary, err := exec.LookPath(kimiCommand)
	if err != nil {
		return titles, errors.Join(errors.Join(failures...), fmt.Errorf("find Kimi Code CLI: %w", err))
	}
	fallbackTitles := make(map[int][]string)
	for cwd, group := range groups {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		sessions, supported, err := listKimiACPSessions(ctx, binary, cwd)
		if err != nil {
			failures = append(failures, fmt.Errorf("list Kimi Code sessions through ACP: %w", err))
			continue
		}
		if !supported {
			continue
		}
		applyKimiSessionTitles(group, fallback, sessions, titles, fallbackTitles)
	}
	if err := resolveKimiFallbackTitles(fallbackTitles, titles); err != nil {
		failures = append(failures, err)
	}
	return titles, errors.Join(failures...)
}

func kimiSessionTitleGroups(identities []registry.ObservationIdentity) (map[string]*kimiTitleGroup, kimiSessionTitleFallback, error) {
	groups := make(map[string]*kimiTitleGroup)
	fallback := kimiSessionTitleFallback{
		indicesByID: make(map[string][]int),
		indices:     make(map[int]bool),
	}
	for i, identity := range identities {
		if identity.SessionID == "" {
			continue
		}
		cwd := strings.TrimSpace(identity.CWD)
		if cwd != "" {
			addKimiTitleIdentity(groups, cwd, identity.SessionID, i)
			continue
		}
		path := identity.SessionPath
		if path == "" {
			var err error
			path, err = kimiCodeSessionPath(identity.SessionID)
			if err != nil {
				return nil, kimiSessionTitleFallback{}, fmt.Errorf("locate Kimi Code session for title lookup: %w", err)
			}
		}
		if !kimiSessionPathMayUseWorkspaceLookup(path, identity.SessionID) {
			continue
		}
		fallback.indicesByID[identity.SessionID] = append(fallback.indicesByID[identity.SessionID], i)
		fallback.indices[i] = true
	}
	return groups, fallback, nil
}

func addKimiTitleIdentity(groups map[string]*kimiTitleGroup, cwd, sessionID string, index int) {
	group := groups[cwd]
	if group == nil {
		group = &kimiTitleGroup{indicesByID: make(map[string][]int)}
		groups[cwd] = group
	}
	group.indicesByID[sessionID] = append(group.indicesByID[sessionID], index)
}

func kimiSessionPathMayUseWorkspaceLookup(sessionPath, sessionID string) bool {
	if sessionPath == "" || filepath.Base(sessionPath) != sessionID {
		return false
	}
	root, err := filepath.Abs(filepath.Join(kimiCodeHome(), "sessions"))
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(sessionPath)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	parts := strings.Split(relative, string(os.PathSeparator))
	return len(parts) == 2 && parts[0] != ".." && parts[1] == sessionID
}

func addKimiFallbackGroups(groups map[string]*kimiTitleGroup, fallback kimiSessionTitleFallback, metadata kimiMetadata) {
	seen := make(map[string]bool)
	for _, workDir := range metadata.WorkDirs {
		if !filepath.IsAbs(workDir.Path) || seen[workDir.Path] {
			continue
		}
		seen[workDir.Path] = true
		for sessionID, indices := range fallback.indicesByID {
			for _, index := range indices {
				addKimiTitleIdentity(groups, workDir.Path, sessionID, index)
			}
		}
	}
}

func applyKimiSessionTitles(
	group *kimiTitleGroup,
	fallback kimiSessionTitleFallback,
	sessions []kimiACPSessionInfo,
	titles []string,
	fallbackTitles map[int][]string,
) {
	for _, session := range sessions {
		for _, index := range group.indicesByID[session.SessionID] {
			title := strings.TrimSpace(session.Title)
			if fallback.indices[index] {
				fallbackTitles[index] = append(fallbackTitles[index], title)
				continue
			}
			titles[index] = title
		}
	}
}

func resolveKimiFallbackTitles(fallbackTitles map[int][]string, titles []string) error {
	var failures []error
	for index, matches := range fallbackTitles {
		if len(matches) > 1 {
			failures = append(failures, fmt.Errorf("%w at index %d", errKimiSessionMatchesMultipleWorkspaces, index))
			continue
		}
		if len(matches) == 1 {
			titles[index] = matches[0]
		}
	}
	return errors.Join(failures...)
}

func readKimiMetadata(ctx context.Context) (kimiMetadata, error) {
	var metadata kimiMetadata
	if err := ctx.Err(); err != nil {
		return metadata, fmt.Errorf("read Kimi Code workspace metadata: %w", err)
	}
	path := filepath.Join(kimiCodeHome(), "kimi.json")
	file, err := titlefile.Open(path)
	if err != nil {
		return metadata, fmt.Errorf("open Kimi Code workspace metadata: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxKimiMetadataBytes+1))
	if err != nil {
		return metadata, fmt.Errorf("read Kimi Code workspace metadata: %w", err)
	}
	if len(data) > maxKimiMetadataBytes {
		return metadata, errKimiMetadataTooLarge
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, fmt.Errorf("decode Kimi Code workspace metadata: %w", err)
	}
	return metadata, nil
}

func listKimiACPSessions(ctx context.Context, binary, cwd string) ([]kimiACPSessionInfo, bool, error) {
	requestCtx, cancel := context.WithTimeout(ctx, kimiACPRequestTimeout)
	defer cancel()
	command := exec.CommandContext(requestCtx, binary, "acp")
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, false, fmt.Errorf("open Kimi ACP input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, false, fmt.Errorf("open Kimi ACP output: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, false, fmt.Errorf("open Kimi ACP diagnostics: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, false, fmt.Errorf("start Kimi ACP server: %w", err)
	}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
		close(stderrDone)
	}()
	defer func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
		<-stderrDone
	}()
	reader := bufio.NewReaderSize(stdout, kimiACPReaderBufferBytes)
	supported, err := initializeKimiACP(requestCtx, stdin, reader)
	if err != nil {
		return nil, false, fmt.Errorf("initialize Kimi ACP: %w", err)
	}
	if !supported {
		return nil, false, nil
	}
	sessions, err := requestKimiACPSessions(requestCtx, stdin, reader, cwd)
	if err != nil {
		return nil, false, fmt.Errorf("list Kimi ACP sessions: %w", err)
	}
	return sessions, true, nil
}

func initializeKimiACP(ctx context.Context, stdin io.Writer, reader *bufio.Reader) (bool, error) {
	if err := writeKimiACPMessage(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      kimiACPInitializeRequestID,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion":    1,
			"clientCapabilities": map[string]any{},
			"clientInfo": map[string]string{
				"name":    "aht",
				"version": "1",
			},
		},
	}); err != nil {
		return false, fmt.Errorf("send initialize request: %w", err)
	}
	initializeResponse, err := readKimiACPResponse(ctx, reader, kimiACPInitializeRequestID)
	if err != nil {
		return false, fmt.Errorf("receive initialize response: %w", err)
	}
	supported, err := kimiACPHasSessionList(initializeResponse)
	if err != nil {
		return false, fmt.Errorf("decode Kimi ACP capabilities: %w", err)
	}
	return supported, nil
}

func requestKimiACPSessions(
	ctx context.Context,
	stdin io.Writer,
	reader *bufio.Reader,
	cwd string,
) ([]kimiACPSessionInfo, error) {
	if err := writeKimiACPMessage(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      kimiACPSessionListRequestID,
		"method":  "session/list",
		"params":  map[string]string{"cwd": cwd},
	}); err != nil {
		return nil, fmt.Errorf("send session list request: %w", err)
	}
	listResponse, err := readKimiACPResponse(ctx, reader, kimiACPSessionListRequestID)
	if err != nil {
		return nil, fmt.Errorf("receive session list response: %w", err)
	}
	sessions, err := decodeKimiACPSessions(listResponse)
	if err != nil {
		return nil, fmt.Errorf("decode Kimi ACP session list: %w", err)
	}
	return sessions, nil
}

func kimiACPHasSessionList(response json.RawMessage) (bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response, &envelope); err != nil {
		return false, fmt.Errorf("decode Kimi ACP initialize response: %w", err)
	}
	agentRaw, ok := envelope["agentCapabilities"]
	if !ok {
		return false, nil
	}
	var agentCapabilities map[string]json.RawMessage
	if err := json.Unmarshal(agentRaw, &agentCapabilities); err != nil {
		return false, fmt.Errorf("decode Kimi ACP agent capabilities: %w", err)
	}
	sessionRaw, ok := agentCapabilities["sessionCapabilities"]
	if !ok {
		return false, nil
	}
	var sessionCapabilities map[string]json.RawMessage
	if err := json.Unmarshal(sessionRaw, &sessionCapabilities); err != nil {
		return false, fmt.Errorf("decode Kimi ACP session capabilities: %w", err)
	}
	list := bytes.TrimSpace(sessionCapabilities["list"])
	return len(list) > 0 && !bytes.Equal(list, []byte("null")), nil
}

func decodeKimiACPSessions(response json.RawMessage) ([]kimiACPSessionInfo, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response, &envelope); err != nil {
		return nil, fmt.Errorf("decode Kimi ACP session list: %w", err)
	}
	sessionRaw, ok := envelope["sessions"]
	if !ok {
		return []kimiACPSessionInfo{}, nil
	}
	var records []json.RawMessage
	if err := json.Unmarshal(sessionRaw, &records); err != nil {
		return nil, fmt.Errorf("decode Kimi ACP session records: %w", err)
	}
	sessions := make([]kimiACPSessionInfo, 0, len(records))
	for _, record := range records {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(record, &fields); err != nil {
			return nil, fmt.Errorf("decode Kimi ACP session: %w", err)
		}
		sessionID, err := kimiACPStringField(fields, "sessionId")
		if err != nil {
			return nil, fmt.Errorf("decode Kimi ACP session identifier: %w", err)
		}
		title, err := kimiACPStringField(fields, "title")
		if err != nil {
			return nil, fmt.Errorf("decode Kimi ACP session title: %w", err)
		}
		sessions = append(sessions, kimiACPSessionInfo{SessionID: sessionID, Title: title})
	}
	return sessions, nil
}

func kimiACPStringField(fields map[string]json.RawMessage, key string) (string, error) {
	raw, ok := fields[key]
	if !ok {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("decode Kimi ACP string field %q: %w", key, err)
	}
	return value, nil
}

func writeKimiACPMessage(writer io.Writer, message any) error {
	if err := json.NewEncoder(writer).Encode(message); err != nil {
		return fmt.Errorf("encode Kimi ACP message: %w", err)
	}
	return nil
}

func readKimiACPResponse(ctx context.Context, reader *bufio.Reader, requestID int) (json.RawMessage, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("receive Kimi ACP response: %w", err)
		}
		line, err := readKimiACPLine(reader)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var message kimiACPMessage
		if err := json.Unmarshal(line, &message); err != nil {
			return nil, fmt.Errorf("decode Kimi ACP message: %w", err)
		}
		if len(message.ID) == 0 {
			continue
		}
		var responseID int
		if err := json.Unmarshal(message.ID, &responseID); err != nil || responseID != requestID {
			continue
		}
		if message.Error != nil {
			return nil, fmt.Errorf("%w with code %d: %s", errKimiACPRequestFailed, message.Error.Code, message.Error.Message)
		}
		if len(message.Result) == 0 {
			return nil, errKimiACPResponseMissingResult
		}
		return message.Result, nil
	}
}

func readKimiACPLine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, kimiACPReaderBufferBytes)
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxKimiACPLineBytes {
			return nil, errKimiACPMessageTooLarge
		}
		line = append(line, part...)
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, fmt.Errorf("read Kimi ACP message line: %w", err)
	}
}
