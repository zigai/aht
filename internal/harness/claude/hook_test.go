package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionStartPrimesNativeTitle(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	path := writeTitleTranscript(
		t,
		"{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"Generated\"}\n",
	)
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork"} {
		t.Run(source, func(t *testing.T) {
			invocation := nativeTitleHook("SessionStart", path)
			invocation.Payload["source"] = source

			result, err := New().HandleHook(t.Context(), invocation)
			if err != nil {
				t.Fatal(err)
			}

			requireNoObservation(t, result)

			if title, err := readSessionTitle(t.Context(), "native", path); err != nil ||
				title != "Generated" {
				t.Fatalf("cached title = %q, %v", title, err)
			}
		})
	}
}

func TestStopRefreshesTitleWithoutRegistryObservation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	path := writeTitleTranscript(t, "")
	for _, step := range []struct {
		change string
		append string
		want   string
	}{
		{"add", "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"Generated\"}\n", "Generated"},
		{"change", "{\"type\":\"custom-title\",\"sessionId\":\"native\",\"customTitle\":\"Manual\"}\n", "Manual"},
		{"change", "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"New generated\"}\n", "Manual"},
		{"change", "{\"type\":\"custom-title\",\"sessionId\":\"native\",\"customTitle\":\"\"}\n", "New generated"},
		{"change", "{\"type\":\"custom-title\",\"sessionId\":\"native\",\"customTitle\":\"Part", "New generated"},
		{"change", "ial\"}\n", "Partial"},
		{"unlink", "", ""},
		{"add", "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"Recreated\"}\n", "Recreated"},
	} {
		changeTitleTranscript(t, path, step.change, step.append)

		result, err := New().HandleHook(t.Context(), nativeTitleHook("Stop", path))
		if err != nil {
			t.Fatal(err)
		}

		requireNoObservation(t, result)

		if title, err := readSessionTitle(t.Context(), "native", path); err != nil ||
			title != step.want {
			t.Fatalf("%s cached title = %q, %v; want %q", step.change, title, err, step.want)
		}
	}
}

func TestNativeTitleHookIgnoresOtherEvents(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	for _, event := range []string{"FileChanged", "PreToolUse", "SessionEnd"} {
		invocation := nativeTitleHook(event, "relative.jsonl")

		result, err := New().HandleHook(t.Context(), invocation)
		if err != nil {
			t.Fatalf("%s: %v", event, err)
		}

		requireNoObservation(t, result)
	}
}

func TestNativeTitleHookAcceptsMissingTranscript(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "absent.jsonl")

	result, err := New().HandleHook(t.Context(), nativeTitleHook("SessionStart", path))
	if err != nil {
		t.Fatal(err)
	}

	requireNoObservation(t, result)
}

func TestNativeTitleHookPropagatesCacheErrors(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	for _, event := range titleHookEvents {
		invocation := nativeTitleHook(event, t.TempDir())
		if result, err := New().HandleHook(t.Context(), invocation); err == nil || result.ReportOK {
			t.Fatalf(
				"%s directory transcript result = %#v, %v; want error without report",
				event,
				result,
				err,
			)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := New().HandleHook(ctx, nativeTitleHook("Stop", writeTitleTranscript(t, "")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hook error = %v", err)
	}
}

func TestNativeTitleHookRejectsInvalidNativeIdentityAndPath(t *testing.T) {
	for _, change := range []struct {
		key   string
		value any
	}{
		{"session_id", " "},
		{"transcript_path", "relative.jsonl"},
		{"transcript_path", ""},
		{"hook_event_name", "PreToolUse"},
	} {
		invocation := nativeTitleHook("Stop", filepath.Join(t.TempDir(), "session.jsonl"))

		invocation.Payload[change.key] = change.value

		result, err := New().HandleHook(t.Context(), invocation)
		if !errors.Is(err, errInvalidTitleHook) || result.ReportOK {
			t.Fatalf("invalid %s result = %#v, %v", change.key, result, err)
		}
	}
}

func requireNoObservation(t *testing.T, result harness.HookResult) {
	t.Helper()

	if result.ReportOK || !reflect.DeepEqual(result.Report, registry.Observation{}) ||
		!reflect.DeepEqual(result.Response, map[string]any{}) {
		t.Fatalf("title hook produced an observation or a native response: %#v", result)
	}
}

func changeTitleTranscript(t *testing.T, path string, change string, content string) {
	t.Helper()

	if change == "unlink" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		return
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func nativeTitleHook(event string, path string) harness.HookInvocation {
	return harness.HookInvocation{
		Event:      event,
		RawPayload: nil,
		ParentArgs: nil,
		Payload: map[string]any{
			"session_id":      "native",
			"transcript_path": path,
			"cwd":             filepath.Dir(path),
			"hook_event_name": event,
		},
	}
}
