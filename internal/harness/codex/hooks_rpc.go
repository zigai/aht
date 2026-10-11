package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var errHookRPC = errors.New("hook RPC failed in Codex")

type hookServer struct {
	encoder  *json.Encoder
	decoder  *json.Decoder
	sequence int
}

type hookRPCResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func runHookServer(ctx context.Context, home string, action func(*hookServer) error) error {
	cmd := exec.CommandContext(ctx, codexCommand, "app-server", "--listen", "stdio://")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("opening Codex app-server input: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Join(fmt.Errorf("opening Codex app-server output: %w", err), stdin.Close())
	}
	if err := cmd.Start(); err != nil {
		return errors.Join(fmt.Errorf("cannot trust aht hooks without Codex; install Codex and ensure codex is on PATH: %w", err), stdin.Close(), stdout.Close())
	}
	server := hookServer{encoder: json.NewEncoder(stdin), decoder: json.NewDecoder(stdout), sequence: 0}
	operationErr := server.initialize()
	if operationErr == nil {
		operationErr = action(&server)
	}
	closeErr := stdin.Close()
	if operationErr != nil {
		killErr := cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			operationErr = errors.Join(operationErr, killErr)
		}
	}
	waitErr := cmd.Wait()
	if operationErr != nil {
		return fmt.Errorf("%w%s", errors.Join(operationErr, closeErr), hookServerDetail(stderr.String()))
	}
	if err := errors.Join(closeErr, waitErr); err != nil {
		return fmt.Errorf("closing Codex app-server: %w%s", err, hookServerDetail(stderr.String()))
	}
	return nil
}

func hookServerDetail(stderr string) string {
	if detail := strings.TrimSpace(stderr); detail != "" {
		return ": " + detail
	}
	return ""
}

func (server *hookServer) initialize() error {
	var result json.RawMessage
	if err := server.request("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "aht", "version": "2"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}, &result); err != nil {
		return err
	}
	if err := server.encoder.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return fmt.Errorf("initializing Codex app-server: %w", err)
	}
	return nil
}

func (server *hookServer) request(method string, params any, result any) error {
	server.sequence++
	if err := server.encoder.Encode(map[string]any{"id": server.sequence, "method": method, "params": params}); err != nil {
		return fmt.Errorf("sending Codex %s request: %w", method, err)
	}
	for {
		var response hookRPCResponse
		if err := server.decoder.Decode(&response); err != nil {
			return fmt.Errorf("reading Codex %s response: %w", method, err)
		}
		if string(response.ID) != strconv.Itoa(server.sequence) {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("%w: %s: %s (code %d)", errHookRPC, method, response.Error.Message, response.Error.Code)
		}
		if len(response.Result) == 0 || string(response.Result) == "null" {
			return fmt.Errorf("%w: %s returned no result", errHookRPC, method)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decoding Codex %s response: %w", method, err)
		}
		return nil
	}
}
