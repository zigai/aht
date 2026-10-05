package claude

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errInvalidTitleHook = errors.New("invalid Claude native title hook")

func (claudeHarness) HandleHook(ctx context.Context, invocation harness.HookInvocation) (harness.HookResult, error) {
	var observation registry.Observation
	result := harness.HookResult{Report: observation, ReportOK: false, Response: map[string]any{}}
	event := invocation.Event
	if event == "" {
		event = harness.PayloadString(invocation.Payload, "hook_event_name")
	}
	if event != harness.HookEventSessionStart && event != "FileChanged" {
		return result, nil
	}
	sessionID, path, err := validateTitleHookIdentity(invocation.Payload, event)
	if err != nil {
		return result, err
	}
	if event == "FileChanged" {
		matches, err := validateTitleFileChange(invocation.Payload, path)
		if err != nil || !matches {
			return result, err
		}
	} else {
		result.Response["hookSpecificOutput"] = map[string]any{
			"hookEventName": harness.HookEventSessionStart,
			"watchPaths":    []string{path},
		}
	}
	_, err = readSessionTitle(ctx, sessionID, path)
	return result, err
}

func validateTitleHookIdentity(payload map[string]any, event string) (string, string, error) {
	if nativeEvent := harness.PayloadString(payload, "hook_event_name"); nativeEvent != event {
		return "", "", fmt.Errorf("%w: hook event %q does not match %q", errInvalidTitleHook, nativeEvent, event)
	}
	sessionID := harness.PayloadString(payload, "session_id")
	path := harness.PayloadString(payload, "transcript_path")
	if strings.TrimSpace(sessionID) == "" || strings.ContainsRune(sessionID, 0) {
		return "", "", fmt.Errorf("%w: session_id is required", errInvalidTitleHook)
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", "", fmt.Errorf("%w: absolute transcript_path is required", errInvalidTitleHook)
	}
	return sessionID, filepath.Clean(path), nil
}

func validateTitleFileChange(payload map[string]any, path string) (bool, error) {
	changedPath := harness.PayloadString(payload, "file_path")
	if !filepath.IsAbs(changedPath) || strings.ContainsRune(changedPath, 0) {
		return false, fmt.Errorf("%w: absolute file_path is required", errInvalidTitleHook)
	}
	if filepath.Clean(changedPath) != path {
		return false, nil
	}
	switch harness.PayloadString(payload, "event") {
	case "change", "add", "unlink":
		return true, nil
	default:
		return false, fmt.Errorf("%w: FileChanged requires change, add, or unlink event", errInvalidTitleHook)
	}
}
