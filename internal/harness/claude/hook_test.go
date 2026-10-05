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

func TestSessionStartWatchesAndPrimesNativeTranscript(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := writeTitleTranscript(t, "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"Generated\"}\n")
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork"} {
		t.Run(source, func(t *testing.T) {
			invocation := nativeTitleHook("SessionStart", path)
			invocation.Payload["source"] = source
			result, err := New().HandleHook(t.Context(), invocation)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "watchPaths": []string{path}}}
			if !reflect.DeepEqual(result.Response, want) || result.ReportOK || !reflect.DeepEqual(result.Report, registry.Observation{}) {
				t.Fatalf("SessionStart result = %#v, want response %#v without observation", result, want)
			}
			if title, err := readSessionTitle(t.Context(), "native", path); err != nil || title != "Generated" {
				t.Fatalf("cached title = %q, %v", title, err)
			}
		})
	}
}

func TestFileChangedMaintainsTitleWithoutRegistryObservation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := writeTitleTranscript(t, "")
	for _, step := range []struct{ event, append, want string }{
		{"add", "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"Generated\"}\n", "Generated"},
		{"change", "{\"type\":\"custom-title\",\"sessionId\":\"native\",\"customTitle\":\"Manual\"}\n", "Manual"},
		{"change", "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"New generated\"}\n", "Manual"},
		{"change", "{\"type\":\"custom-title\",\"sessionId\":\"native\",\"customTitle\":\"\"}\n", "New generated"},
		{"change", "{\"type\":\"custom-title\",\"sessionId\":\"native\",\"customTitle\":\"Part", "New generated"},
		{"change", "ial\"}\n", "Partial"},
		{"unlink", "", ""},
		{"add", "{\"type\":\"ai-title\",\"sessionId\":\"native\",\"aiTitle\":\"Recreated\"}\n", "Recreated"},
	} {
		changeTitleTranscript(t, path, step.event, step.append)
		invocation := nativeTitleHook("FileChanged", path)
		invocation.Payload["file_path"] = filepath.Dir(path) + "/./" + filepath.Base(path)
		invocation.Payload["event"] = step.event
		result, err := New().HandleHook(t.Context(), invocation)
		if err != nil {
			t.Fatal(err)
		}
		if result.ReportOK || !reflect.DeepEqual(result.Report, registry.Observation{}) || !reflect.DeepEqual(result.Response, map[string]any{}) {
			t.Fatalf("FileChanged produced registry observation or native side effects: %#v", result)
		}
		if title, err := readSessionTitle(t.Context(), "native", path); err != nil || title != step.want {
			t.Fatalf("%s cached title = %q, %v; want %q", step.event, title, err, step.want)
		}
	}
}

func TestNativeTitleHookIgnoresOtherWatchedFiles(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	invocation := nativeTitleHook("FileChanged", t.TempDir())
	invocation.Payload["file_path"] = filepath.Join(t.TempDir(), "other")
	invocation.Payload["event"] = "change"
	result, err := New().HandleHook(t.Context(), invocation)
	if err != nil || result.ReportOK || !reflect.DeepEqual(result.Response, map[string]any{}) {
		t.Fatalf("unrelated watched file result = %#v, %v", result, err)
	}
}

func TestNativeTitleHookRegistersMissingTranscript(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "absent.jsonl")
	result, err := New().HandleHook(t.Context(), nativeTitleHook("SessionStart", path))
	want := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "watchPaths": []string{path}}}
	if err != nil || !reflect.DeepEqual(result.Response, want) || result.ReportOK {
		t.Fatalf("missing transcript result = %#v, %v", result, err)
	}
}

func TestNativeTitleHookPropagatesCacheErrors(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, event := range []string{"SessionStart", "FileChanged"} {
		invocation := nativeTitleHook(event, t.TempDir())
		invocation.Payload["file_path"] = invocation.Payload["transcript_path"]
		invocation.Payload["event"] = "change"
		if result, err := New().HandleHook(t.Context(), invocation); err == nil || result.ReportOK {
			t.Fatalf("%s directory transcript result = %#v, %v; want error without report", event, result, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := New().HandleHook(ctx, nativeTitleHook("SessionStart", writeTitleTranscript(t, "")))
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
		{"hook_event_name", "Stop"},
	} {
		invocation := nativeTitleHook("SessionStart", filepath.Join(t.TempDir(), "session.jsonl"))
		invocation.Payload[change.key] = change.value
		if result, err := New().HandleHook(t.Context(), invocation); err == nil || result.ReportOK {
			t.Fatalf("invalid %s result = %#v, %v", change.key, result, err)
		}
	}
}

func TestFileChangedRejectsInvalidChangedPathAndEvent(t *testing.T) {
	for _, change := range []struct {
		key   string
		value string
	}{
		{"file_path", "relative.jsonl"},
		{"file_path", ""},
		{"event", "rename"},
		{"event", ""},
	} {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		invocation := nativeTitleHook("FileChanged", path)
		invocation.Payload["file_path"] = path
		invocation.Payload["event"] = "change"
		invocation.Payload[change.key] = change.value
		result, err := New().HandleHook(t.Context(), invocation)
		if !errors.Is(err, errInvalidTitleHook) || result.ReportOK || !reflect.DeepEqual(result.Response, map[string]any{}) {
			t.Fatalf("invalid %s result = %#v, %v", change.key, result, err)
		}
	}
}

func changeTitleTranscript(t *testing.T, path, event, content string) {
	t.Helper()
	if event == "unlink" {
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

func nativeTitleHook(event, path string) harness.HookInvocation {
	return harness.HookInvocation{Event: event, RawPayload: nil, ParentArgs: nil, Payload: map[string]any{
		"session_id": "native", "transcript_path": path, "cwd": filepath.Dir(path), "hook_event_name": event,
	}}
}
