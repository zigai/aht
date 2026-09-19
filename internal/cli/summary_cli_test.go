package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestListSummaryFlagValidation(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	ctx := t.Context()

	// 1. --group-by without --summary must fail at parse boundary with exit code 2
	{
		var stdout, stderr bytes.Buffer
		code := executeCLI(ctx, []string{"--store", storePath, "list", "--group-by", "project"}, strings.NewReader(""), &stdout, &stderr)
		if code != exitCodeUsage {
			t.Fatalf("exit code = %d, want %d (exitCodeUsage)", code, exitCodeUsage)
		}
		if !strings.Contains(stderr.String(), "--group-by requires --summary") {
			t.Fatalf("stderr = %q, want '--group-by requires --summary'", stderr.String())
		}
	}

	// 2. --summary with unsupported --group-by value must fail with exit code 2
	{
		var stdout, stderr bytes.Buffer
		code := executeCLI(ctx, []string{"--store", storePath, "list", "--summary", "--group-by", "unsupported-dim"}, strings.NewReader(""), &stdout, &stderr)
		if code != exitCodeUsage {
			t.Fatalf("exit code = %d, want %d (exitCodeUsage)", code, exitCodeUsage)
		}
		if !strings.Contains(stderr.String(), "unsupported summary group-by") {
			t.Fatalf("stderr = %q, want error about unsupported group-by", stderr.String())
		}
	}

	// 3. --summary with invalid sort flags must fail with exit code 2
	{
		var stdout, stderr bytes.Buffer
		code := executeCLI(ctx, []string{"--store", storePath, "list", "--summary", "--sort", "updated"}, strings.NewReader(""), &stdout, &stderr)
		if code != exitCodeUsage {
			t.Fatalf("exit code = %d, want %d (exitCodeUsage)", code, exitCodeUsage)
		}
	}
}

func setupSummaryTableFixture(t *testing.T) (string, context.Context) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(storePath)
	ctx := t.Context()

	running := registry.ActivityRunning
	idle := registry.ActivityIdle
	waiting := registry.ActivityWaiting

	obs := []registry.Observation{
		{
			Source:     registry.ObservationSourceNative,
			Evidence:   registry.ObservationEvidenceNativeEvent,
			Harness:    registry.HarnessClaude,
			Identity:   registry.ObservationIdentity{SessionID: "sess-1"},
			Presence:   new(registry.PresenceLive),
			Activity:   &running,
			Catalog:    &registry.CatalogMetadata{ProjectRoot: "/home/alice/service"},
			Tmux:       &registry.TmuxContext{SessionName: "main"},
			ObservedAt: time.Now().UTC(),
		},
		{
			Source:     registry.ObservationSourceNative,
			Evidence:   registry.ObservationEvidenceNativeEvent,
			Harness:    registry.HarnessClaude,
			Identity:   registry.ObservationIdentity{SessionID: "sess-2"},
			Presence:   new(registry.PresenceLive),
			Activity:   &idle,
			Catalog:    &registry.CatalogMetadata{ProjectRoot: "/home/bob/service"},
			Tmux:       &registry.TmuxContext{SessionName: "worker"},
			ObservedAt: time.Now().UTC(),
		},
		{
			Source:     registry.ObservationSourceNative,
			Evidence:   registry.ObservationEvidenceNativeEvent,
			Harness:    registry.HarnessCodex,
			Identity:   registry.ObservationIdentity{SessionID: "sess-3"},
			Presence:   new(registry.PresenceGone),
			Catalog:    &registry.CatalogMetadata{ProjectRoot: "/home/carol/app"},
			Tmux:       &registry.TmuxContext{SessionName: "main"},
			ObservedAt: time.Now().UTC(),
		},
		{
			Source:     registry.ObservationSourceNative,
			Evidence:   registry.ObservationEvidenceNativeEvent,
			Harness:    registry.HarnessOmp,
			Identity:   registry.ObservationIdentity{SessionID: "sess-4"},
			Presence:   new(registry.PresenceLive),
			Activity:   &waiting,
			ObservedAt: time.Now().UTC(),
		},
	}
	if _, err := store.ObserveBatch(ctx, obs); err != nil {
		t.Fatal(err)
	}
	return storePath, ctx
}

func TestListSummaryTableRendering(t *testing.T) {
	t.Parallel()
	storePath, ctx := setupSummaryTableFixture(t)

	t.Run("project", func(t *testing.T) {
		testListSummaryProjectTable(t, ctx, storePath)
	})
	t.Run("harness", func(t *testing.T) {
		testListSummaryHarnessTable(t, ctx, storePath)
	})
	t.Run("multiplexer_session", func(t *testing.T) {
		testListSummaryMultiplexerTable(t, ctx, storePath)
	})
	t.Run("default_summary", func(t *testing.T) {
		testListSummaryDefaultTable(t, ctx, storePath)
	})
}

func testListSummaryProjectTable(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "list", "--summary", "--group-by", "project"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runTestCLI project summary failed: %v", err)
	}
	out := stdout.String()
	for _, heading := range []string{"Project", "Root", "Total", "Live", "Gone", "Pres?", "Run", "Wait", "Idle", "Failed", "Interrupted", "Act?"} {
		if !strings.Contains(out, heading) {
			t.Errorf("project summary table missing heading %q\n%s", heading, out)
		}
	}
	if !strings.Contains(out, "/home/alice/service") || !strings.Contains(out, "/home/bob/service") {
		t.Errorf("colliding basenames not shown distinctly with full root\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("missing project root not shown\n%s", out)
	}
}

func testListSummaryHarnessTable(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "list", "--summary", "--group-by", "harness"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runTestCLI harness summary failed: %v", err)
	}
	out := stdout.String()
	for _, heading := range []string{"Agent", "Total", "Live", "Gone", "Pres?", "Run", "Wait", "Idle", "Failed", "Interrupted", "Act?"} {
		if !strings.Contains(out, heading) {
			t.Errorf("harness summary table missing heading %q\n%s", heading, out)
		}
	}
	for _, harness := range []string{"claude", "codex", "omp"} {
		if !strings.Contains(out, harness) {
			t.Errorf("harness %q not found in output\n%s", harness, out)
		}
	}
}

func testListSummaryMultiplexerTable(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "list", "--summary", "--group-by", "multiplexer-session"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runTestCLI multiplexer summary failed: %v", err)
	}
	out := stdout.String()
	for _, heading := range []string{"MUX", "Session", "Server", "Total", "Live", "Gone"} {
		if !strings.Contains(out, heading) {
			t.Errorf("multiplexer summary table missing heading %q\n%s", heading, out)
		}
	}
	for _, session := range []string{"main", "worker", "unknown"} {
		if !strings.Contains(out, session) {
			t.Errorf("session %q not found in multiplexer output\n%s", session, out)
		}
	}
}

func testListSummaryDefaultTable(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "list", "--summary"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runTestCLI default summary failed: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "MUX") || !strings.Contains(out, "Session") {
		t.Errorf("default summary did not use multiplexer session layout\n%s", out)
	}
}

func setupSummaryJSONFixture(t *testing.T) (string, context.Context) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(storePath)
	ctx := t.Context()

	running := registry.ActivityRunning
	obs := []registry.Observation{
		{
			Source:     registry.ObservationSourceNative,
			Evidence:   registry.ObservationEvidenceNativeEvent,
			Harness:    registry.HarnessClaude,
			Identity:   registry.ObservationIdentity{SessionID: "sess-1"},
			Presence:   new(registry.PresenceLive),
			Activity:   &running,
			Catalog:    &registry.CatalogMetadata{ProjectRoot: "/home/dev/myapp"},
			Tmux:       &registry.TmuxContext{SessionName: "work"},
			ObservedAt: time.Now().UTC(),
		},
	}
	if _, err := store.ObserveBatch(ctx, obs); err != nil {
		t.Fatal(err)
	}
	return storePath, ctx
}

func TestListSummaryJSONRendering(t *testing.T) {
	t.Parallel()
	storePath, ctx := setupSummaryJSONFixture(t)

	t.Run("project", func(t *testing.T) {
		testListSummaryProjectJSON(t, ctx, storePath)
	})
	t.Run("harness", func(t *testing.T) {
		testListSummaryHarnessJSON(t, ctx, storePath)
	})
	t.Run("default_summary", func(t *testing.T) {
		testListSummaryDefaultJSON(t, ctx, storePath)
	})
}

func testListSummaryProjectJSON(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "--json", "list", "--summary", "--group-by", "project"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("JSON project summary error: %v", err)
	}
	var summaries []registry.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summaries); err != nil {
		t.Fatalf("Unmarshal error: %v\nstdout: %s", err, stdout.String())
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].GroupBy != registry.SummaryGroupByProject {
		t.Errorf("GroupBy = %q, want %q", summaries[0].GroupBy, registry.SummaryGroupByProject)
	}
	if summaries[0].Project != "myapp" || summaries[0].ProjectRoot != "/home/dev/myapp" {
		t.Errorf("project fields mismatch: %+v", summaries[0])
	}
	if summaries[0].Total != 1 || summaries[0].Live != 1 || summaries[0].Running != 1 {
		t.Errorf("counts mismatch: %+v", summaries[0])
	}
}

func testListSummaryHarnessJSON(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "--json", "list", "--summary", "--group-by", "harness"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("JSON harness summary error: %v", err)
	}
	var summaries []registry.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summaries); err != nil {
		t.Fatalf("Unmarshal error: %v\nstdout: %s", err, stdout.String())
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].GroupBy != registry.SummaryGroupByHarness {
		t.Errorf("GroupBy = %q, want %q", summaries[0].GroupBy, registry.SummaryGroupByHarness)
	}
	if summaries[0].Harness != registry.HarnessClaude {
		t.Errorf("Harness = %q, want %q", summaries[0].Harness, registry.HarnessClaude)
	}
}

func testListSummaryDefaultJSON(t *testing.T, ctx context.Context, storePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runTestCLI(ctx, []string{"--store", storePath, "--json", "list", "--summary"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("JSON default summary error: %v", err)
	}
	var summaries []registry.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summaries); err != nil {
		t.Fatalf("Unmarshal error: %v\nstdout: %s", err, stdout.String())
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].MultiplexerSessionName != "work" && summaries[0].TmuxSessionName != "work" {
		t.Errorf("legacy mux session name missing: %+v", summaries[0])
	}
	if summaries[0].Total != 1 || summaries[0].Live != 1 {
		t.Errorf("counts mismatch: %+v", summaries[0])
	}
}

func TestListSummaryEmptyState(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	ctx := t.Context()

	// Empty store table output prints "No results."
	for _, groupBy := range []string{"project", "harness", "multiplexer-session"} {
		var stdout, stderr bytes.Buffer
		err := runTestCLI(ctx, []string{"--store", storePath, "list", "--summary", "--group-by", groupBy}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("empty summary error for %s: %v", groupBy, err)
		}
		if got := stdout.String(); got != "No results.\n" {
			t.Fatalf("group %s: expected 'No results.\\n', got %q", groupBy, got)
		}
	}
}
