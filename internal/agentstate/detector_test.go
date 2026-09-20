package agentstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

func TestGoldenScreenFixtures(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "golden_screens.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Agent  string            `json:"agent"`
		Name   string            `json:"name"`
		Screen string            `json:"screen"`
		Want   registry.Activity `json:"want"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Agent+"/"+fixture.Name, func(t *testing.T) {
			t.Parallel()
			harness, err := harnesscatalog.Normalize(fixture.Agent)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := (Loader{ConfigDir: t.TempDir()}).Load(harness)
			if err != nil {
				t.Fatal(err)
			}
			decision := manifest.Evaluate(NormalizeSnapshot(fixture.Screen, fixture.Agent))
			if decision.Activity != fixture.Want {
				t.Fatalf("decision = %#v, want %s", decision, fixture.Want)
			}
			inspection := manifest.Inspect(NormalizeSnapshot(fixture.Screen, fixture.Agent))
			if diff := cmp.Diff(decision, inspection.Decision); diff != "" {
				t.Fatalf("inspection decision differs from evaluation (-evaluate +inspect):\n%s", diff)
			}
		})
	}
}

func TestBundledManifestsClassifyTargetAgents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		harness registry.Harness
		screen  string
		want    registry.Activity
		rule    string
	}{
		{registry.HarnessCodex, "› implement this\nContext 63% used", registry.ActivityIdle, "input_prompt"},
		{registry.HarnessCodex, "Would you like to run the following command?", registry.ActivityWaiting, "permission_prompt"},
		{registry.HarnessCodex, "API error: Rate limit reached", registry.ActivityFailed, "error_prompt"},
		{registry.HarnessCodex, "Operation cancelled by user", registry.ActivityInterrupted, "interrupted_prompt"}, //nolint:misspell // Fixture mirrors Codex output.
		{registry.HarnessClaude, "Thinking… esc to interrupt", registry.ActivityRunning, "working_interruptible"},
		{registry.HarnessClaude, "❯ \n? for shortcuts", registry.ActivityIdle, "input_prompt"},
		{registry.HarnessClaude, "API Error: Rate limit exceeded", registry.ActivityFailed, "error_prompt"},
		{registry.HarnessClaude, "Claude was interrupted", registry.ActivityInterrupted, "interrupted_prompt"},
		{registry.HarnessOpenCode, "Permission required: allow / deny", registry.ActivityWaiting, "permission_prompt"},
		{registry.HarnessOpenCode, "Ask anything", registry.ActivityIdle, "input_prompt"},
		{registry.HarnessOpenCode, "API error: Execution error", registry.ActivityFailed, "error_prompt"},
		{registry.HarnessOpenCode, "Stopped by user", registry.ActivityInterrupted, "interrupted_prompt"},
		{registry.HarnessPi, "Working · esc to interrupt", registry.ActivityRunning, "working_interruptible"},
		{registry.HarnessPi, "Type a message · Enter to send", registry.ActivityIdle, "input_prompt"},
		{registry.HarnessPi, "API Error: Rate limit exceeded", registry.ActivityFailed, "error_prompt"},
		{registry.HarnessPi, "Interrupted by user", registry.ActivityInterrupted, "interrupted_prompt"},
		{registry.HarnessOmp, " ⠋ Working... (40s)", registry.ActivityRunning, "custom_working"},
		{registry.HarnessOmp, " ~/Projects/sesh · Codex · GPT-5.6 Sol · medium 7.1%/1M" + strings.Repeat("\n ", 20), registry.ActivityIdle, "custom_input_prompt"},
		{registry.HarnessOmp, "Permission required: allow / deny", registry.ActivityWaiting, "permission_prompt"},
		{registry.HarnessOmp, "API Error: Rate limit exceeded", registry.ActivityFailed, "error_prompt"},
		{registry.HarnessOmp, "Interrupted by user", registry.ActivityInterrupted, "interrupted_prompt"},
	}
	for _, test := range tests {
		t.Run(string(test.harness)+"/"+test.rule, func(t *testing.T) {
			t.Parallel()
			manifest, err := (Loader{ConfigDir: t.TempDir()}).Load(test.harness)
			if err != nil {
				t.Fatal(err)
			}
			decision := manifest.Evaluate(NormalizeSnapshot(test.screen, ""))
			if decision.Activity != test.want || decision.RuleID != test.rule {
				t.Fatalf("decision = %#v, want activity %q rule %q", decision, test.want, test.rule)
			}
		})
	}
}

func TestBundledManifestScenarioBoundaries(t *testing.T) {
	t.Parallel()

	const piFooter = " ~/Projects · Codex · GPT-5.6 Sol · max 18.3%/272k"
	tests := []struct {
		name    string
		harness registry.Harness
		screen  string
		want    registry.Activity
		rule    string
	}{
		{
			name: "codex case insensitive permission", harness: registry.HarnessCodex,
			screen: "would you like to run the following command?", want: registry.ActivityWaiting, rule: "permission_prompt",
		},
		{
			name: "codex permission outside region", harness: registry.HarnessCodex,
			screen: "Would you like to run the following command?\n" + strings.Repeat("ordinary output\n", 30),
			want:   registry.ActivityUnknown,
		},
		{
			name: "codex historical error while running", harness: registry.HarnessCodex,
			screen: "API error: old failure\nRunning command · esc to interrupt",
			want:   registry.ActivityRunning, rule: "working_interruptible",
		},
		{
			name: "pi ANSI custom footer", harness: registry.HarnessPi,
			screen: "\x1b[2m" + piFooter + "\x1b[0m", want: registry.ActivityIdle, rule: "custom_input_prompt",
		},
		{
			name: "pi custom footer outside region", harness: registry.HarnessPi,
			screen: piFooter + "\n" + strings.Repeat("ordinary output\n", 12),
			want:   registry.ActivityUnknown,
		},
		{
			name: "pi exact historical working text", harness: registry.HarnessPi,
			screen: "Working...\n" + piFooter, want: registry.ActivityIdle, rule: "custom_input_prompt",
		},
		{
			name: "omp custom working with trailing action text", harness: registry.HarnessOmp,
			screen: "  ⠋ Working... (2m 9s) Running build\n" + piFooter,
			want:   registry.ActivityRunning, rule: "custom_working",
		},
		{
			name: "omp custom working suppresses custom footer idle", harness: registry.HarnessOmp,
			screen: "  ⠹ Working... (16m 30s) Running test suite\n" + piFooter,
			want:   registry.ActivityRunning, rule: "custom_working",
		},
		{
			name: "pi custom working with trailing action text", harness: registry.HarnessPi,
			screen: "  ⠋ Working... (40s) Running tests\n" + piFooter,
			want:   registry.ActivityRunning, rule: "custom_working",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, err := (Loader{ConfigDir: t.TempDir()}).Load(test.harness)
			if err != nil {
				t.Fatal(err)
			}
			decision := manifest.Evaluate(NormalizeSnapshot(test.screen, ""))
			if decision.Activity != test.want || decision.RuleID != test.rule {
				t.Fatalf("decision = %#v, want activity %q rule %q", decision, test.want, test.rule)
			}
		})
	}
}

func TestNormalizeSnapshotStripsTerminalEscapesAndBoundsHistory(t *testing.T) {
	t.Parallel()
	lines := make([]string, maxSnapshotLines+5)
	for index := range lines {
		lines[index] = "line"
	}
	lines[len(lines)-1] = "\x1b[31mREADY\x1b[0m"
	snapshot := NormalizeSnapshot(strings.Join(lines, "\n"), "\x1b]0;Codex\a")
	if len(snapshot.Lines) != maxSnapshotLines || snapshot.Lines[len(snapshot.Lines)-1] != "READY" || snapshot.Title != "" {
		t.Fatalf("normalized snapshot = %#v", snapshot)
	}
	blankRows := NormalizeSnapshot("permission\n\n\n", "")
	if len(blankRows.Lines) != 3 || blankRows.Lines[0] != "permission" || blankRows.Lines[1] != "" || blankRows.Lines[2] != "" {
		t.Fatalf("trailing blank rows were not preserved: %#v", blankRows)
	}
}

func TestDetectorIsConservativeWhenNoRuleMatches(t *testing.T) {
	t.Parallel()
	manifest, err := (Loader{ConfigDir: t.TempDir()}).Load(registry.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	decision := manifest.Evaluate(NormalizeSnapshot("ordinary shell output", "shell"))
	if decision.Activity != registry.ActivityUnknown || decision.Reason != "no_rule_matched" {
		t.Fatalf("decision = %#v, want unknown/no_rule_matched", decision)
	}
}

func TestRulePriorityRegionRegexAndExclusion(t *testing.T) {
	t.Parallel()
	manifest, err := ParseManifest([]byte(`version=1
agent="codex"
[[rules]]
id="low"
state="idle"
priority=1
region="all"
any=["READY"]
[[rules]]
id="high"
state="waiting"
priority=20
region="bottom:2"
all=["Approval"]
none=["do not prompt"]
regex_all=["APPROVAL"]
regex_any=["ALLOW|DENY"]
regex_none=["RESOLVED"]
title_any=["codex"]
title_regex_any=["^CODEX"]
`), registry.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	decision := manifest.Evaluate(NormalizeSnapshot("READY\nApproval needed: Allow or Deny", "Codex task"))
	if decision.RuleID != "high" || decision.Activity != registry.ActivityWaiting {
		t.Fatalf("decision = %#v, want high/waiting", decision)
	}
	if excluded := manifest.Evaluate(NormalizeSnapshot("READY\nApproval needed: Allow or Deny; do not prompt", "Codex task")); excluded.RuleID != "low" {
		t.Fatalf("literal exclusion did not reject high rule: %#v", excluded)
	}
	manifest.Rules[0].Priority = 20
	manifest.Rules[1].Priority = 20
	if stable := manifest.Evaluate(NormalizeSnapshot("READY\nApproval needed: Allow or Deny", "Codex task")); stable.RuleID != "low" {
		t.Fatalf("equal-priority order was not stable: %#v", stable)
	}
	snapshot := NormalizeSnapshot("READY\nApproval needed: Allow or Deny", "Codex task")
	if diff := cmp.Diff(manifest.Evaluate(snapshot), manifest.Inspect(snapshot).Decision); diff != "" {
		t.Fatalf("inspection after priority mutation differs (-evaluate +inspect):\n%s", diff)
	}
}

func TestManifestRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	_, err := ParseManifest([]byte("version=1\nagent='pi'\nunknown='typo'\n[[rules]]\nid='idle'\nstate='idle'\nany=['ready']\n"), registry.HarnessPi)
	if err == nil || !strings.Contains(err.Error(), errManifestInvalid.Error()) {
		t.Fatalf("unknown manifest field error = %v", err)
	}
	if _, err := ParseManifest(make([]byte, maxManifestBytes+1), registry.HarnessPi); err == nil || !strings.Contains(err.Error(), errManifestTooLarge.Error()) {
		t.Fatalf("oversized manifest error = %v", err)
	}
}

func TestManifestRejectsEmptyMatchers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matcher string
	}{
		{name: "all", matcher: "all=['']"},
		{name: "any", matcher: "any=['  ']"},
		{name: "none", matcher: "any=['ready']\nnone=['']"},
		{name: "regex all", matcher: "regex_all=['']"},
		{name: "regex any", matcher: "regex_any=['  ']"},
		{name: "regex none", matcher: "any=['ready']\nregex_none=['']"},
		{name: "title any", matcher: "title_any=['']"},
		{name: "title regex any", matcher: "title_regex_any=['  ']"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := []byte("version=1\nagent='pi'\n[[rules]]\nid='invalid'\nstate='idle'\n" + test.matcher + "\n")
			if _, err := ParseManifest(data, registry.HarnessPi); err == nil || !strings.Contains(err.Error(), "is empty") {
				t.Fatalf("ParseManifest() error = %v, want empty matcher rejection", err)
			}
		})
	}
}

func TestLoaderUsesValidOverrideAndFallsBackFromInvalidOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pi.toml")
	if err := os.WriteFile(path, []byte("version=1\nagent='pi'\n[[rules]]\nid='custom'\nstate='idle'\nany=['CUSTOM READY']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := (Loader{ConfigDir: dir}).Load(registry.HarnessPi)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source != path || manifest.Evaluate(NormalizeSnapshot("CUSTOM READY", "")).RuleID != "custom" {
		t.Fatalf("valid override not used: %#v", manifest)
	}
	if err := os.WriteFile(path, []byte("not valid ["), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err = (Loader{ConfigDir: dir}).Load(registry.HarnessPi)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(manifest.Source, "bundled:") || manifest.Warning == "" {
		t.Fatalf("invalid override did not fall back with warning: %#v", manifest)
	}
}

func TestLoaderUsesLocalOnlyAgyOverrideWithoutChangingDefaultSupport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	loader := Loader{ConfigDir: dir}
	if loader.Supports(registry.HarnessAgy) {
		t.Fatal("Agy screen detection should require a local override")
	}
	if _, err := loader.Load(registry.HarnessAgy); err == nil {
		t.Fatal("Agy screen manifest loaded without a local override")
	}

	path := filepath.Join(dir, "agy.toml")
	if err := os.WriteFile(path, []byte("version=1\nagent='agy'\n[[rules]]\nid='custom_footer'\nstate='idle'\nany=['ready']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !loader.Supports(registry.HarnessAgy) {
		t.Fatal("local Agy override did not enable screen detection")
	}
	manifest, err := loader.Load(registry.HarnessAgy)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source != path || manifest.Evaluate(NormalizeSnapshot("ready", "")).RuleID != "custom_footer" {
		t.Fatalf("local-only Agy override not used: %#v", manifest)
	}
}

func TestDecisionJSONNeverContainsScreenContents(t *testing.T) {
	t.Parallel()
	const secret = "SUPER-SECRET-PROMPT"
	manifest, err := (Loader{ConfigDir: t.TempDir()}).Load(registry.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest.Evaluate(NormalizeSnapshot(secret, secret)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("decision persisted screen contents: %s", encoded)
	}
}

//nolint:cyclop // assertions cover each integration validity and freshness reason
func TestHookAuthorityRequiresMatchingProcess(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	process := registry.ProcessIdentity{PID: 12, StartIdentity: "boot:12"}
	running := registry.ActivityRunning
	session := registry.Session{Harness: registry.HarnessPi, Presence: registry.PresenceLive, Process: &process, Observations: registry.Observations{Native: &registry.NativeObservation{Activity: &running, Attributes: map[string]string{"aht_integration": "pi-extension"}, Process: process, ObservedAt: now}}}
	if !HookIsActive(session, now) || ShouldDetectScreen(session, now) {
		t.Fatal("matching Pi extension report was not authoritative")
	}
	session.Observations.Native.Process.StartIdentity = "old"
	if HookIsActive(session, now) || !ShouldDetectScreen(session, now) {
		t.Fatal("stale Pi extension report did not fall back to screen")
	}
	session.Observations.Native.Process = process
	gone := registry.PresenceGone
	session.Observations.Native.Presence = &gone
	if evaluation := EvaluateHook(session, now); evaluation.Active || evaluation.Reason != "integration_ended" {
		t.Fatalf("ended integration evaluation = %#v", evaluation)
	}
	session.Observations.Native.Presence = nil
	session.Observations.Native.ObservedAt = now.Add(time.Second)
	if evaluation := EvaluateHook(session, now); evaluation.Active || !evaluation.ProcessMatches || evaluation.Reason != "integration_observation_from_future" {
		t.Fatalf("future integration evaluation = %#v", evaluation)
	}
	session.Observations.Native.ObservedAt = now.Add(-registry.IntegrationActivityLease - time.Second)
	if evaluation := EvaluateHook(session, now); evaluation.Active || evaluation.Fresh || !evaluation.ProcessMatches || evaluation.Reason != "integration_report_stale" || !ShouldDetectScreen(session, now) {
		t.Fatalf("stale integration evaluation = %#v", evaluation)
	}
	if PolicyFor(registry.HarnessCodex).Primary != AuthorityScreen {
		t.Fatal("Codex must be screen authoritative")
	}
}

func TestOmpHookAuthorityUsesNativeIntegration(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	session := ompSession(now)

	policy := PolicyFor(registry.HarnessOmp)
	if policy.Primary != AuthorityHook || !policy.ScreenFallback || policy.IntegrationValue != "omp-extension" {
		t.Fatalf("OMP policy = %#v", policy)
	}
	evaluation := EvaluateHook(session, now)
	if !evaluation.Active || !evaluation.Fresh || !evaluation.ProcessMatches || evaluation.Reason != "matching_live_process_report" {
		t.Fatalf("OMP hook evaluation = %#v", evaluation)
	}
	if ShouldDetectScreen(session, now) {
		t.Fatal("fresh OMP hook should not fall back to screen detection")
	}
}

func TestOmpHookAuthorityFallsBackToScreenWhenStale(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	session := ompSession(now.Add(-registry.IntegrationActivityLease - time.Second))

	evaluation := EvaluateHook(session, now)
	if evaluation.Active || evaluation.Fresh || !evaluation.ProcessMatches || evaluation.Reason != "integration_report_stale" {
		t.Fatalf("stale OMP hook evaluation = %#v", evaluation)
	}
	if !ShouldDetectScreen(session, now) {
		t.Fatal("stale OMP hook must fall back to screen detection")
	}
}

func ompSession(observedAt time.Time) registry.Session {
	process := registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	running := registry.ActivityRunning
	return registry.Session{
		Harness:  registry.HarnessOmp,
		Presence: registry.PresenceLive,
		Process:  &process,
		Observations: registry.Observations{Native: &registry.NativeObservation{
			Activity:   &running,
			Attributes: map[string]string{"aht_integration": "omp-extension"},
			Process:    process,
			ObservedAt: observedAt,
		}},
	}
}

//nolint:cyclop,gocognit // comprehensive scenario boundaries for inspection
func TestInspectDetection(t *testing.T) {
	t.Parallel()

	tomlManifest := []byte(`version=1
agent="codex"

[[rules]]
id="title_rule"
state="waiting"
priority=90
region="all"
title_any=["APPROVAL REQUIRED"]
title_regex_any=["^APPROVAL"]

[[rules]]
id="winner_rule"
state="waiting"
priority=80
region="bottom:3"
all=["ALLOW", "DENY"]
any=["Allow", "Please confirm"]
none=["auto-reject"]
regex_all=["ALLOW.*DENY"]
regex_any=["ALLOW|CONFIRM"]
regex_none=["EXCLUDED_REGEX"]

[[rules]]
id="shadowed_equal"
state="idle"
priority=80
region="all"
any=["Allow"]

[[rules]]
id="shadowed_lower"
state="idle"
priority=50
region="all"
any=["Allow"]

[[rules]]
id="negative_literal_fail"
state="running"
priority=40
region="all"
any=["Allow"]
none=["DENY"]

[[rules]]
id="negative_regex_fail"
state="running"
priority=35
region="all"
any=["Allow"]
regex_none=["DENY"]

[[rules]]
id="top_region_rule"
state="running"
priority=30
region="top:2"
any=["HEADER_MARKER"]
`)

	manifest, err := ParseManifest(tomlManifest, registry.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("winning rule, priority ties, shadowing, and negative matchers", func(t *testing.T) {
		t.Parallel()
		screen := "\x1b[32mHEADER_NOT_IN_BOTTOM\x1b[0m\n" +
			"Line 2: ordinary line\n" +
			"Line 3: ordinary line\n" +
			"Please confirm: ALLOW or DENY the action\n\n"
		snapshot := NormalizeSnapshot(screen, "Ordinary Task")

		inspection := manifest.Inspect(snapshot)
		if inspection.Decision.Activity != registry.ActivityWaiting || inspection.WinningRule != "winner_rule" {
			t.Fatalf("inspection = %#v, want winner_rule/waiting", inspection)
		}
		if inspection.LinesEvaluated != 5 {
			t.Fatalf("lines evaluated = %d, want 5", inspection.LinesEvaluated)
		}

		candidatesByID := make(map[string]CandidateRule, len(inspection.Candidates))
		for _, c := range inspection.Candidates {
			candidatesByID[c.ID] = c
		}

		winner := candidatesByID["winner_rule"]
		if !winner.Matched || !winner.Winner || winner.Reason != "matched" {
			t.Fatalf("winner rule = %#v", winner)
		}

		equalShadowed := candidatesByID["shadowed_equal"]
		if !equalShadowed.Matched || equalShadowed.Winner {
			t.Fatalf("equal priority rule matched/winner state = %#v", equalShadowed)
		}
		if !strings.Contains(equalShadowed.Reason, "equal priority 80, earlier in manifest") {
			t.Fatalf("equal priority rule reason = %q", equalShadowed.Reason)
		}

		lowerShadowed := candidatesByID["shadowed_lower"]
		if !lowerShadowed.Matched || lowerShadowed.Winner {
			t.Fatalf("lower priority rule matched/winner state = %#v", lowerShadowed)
		}
		if !strings.Contains(lowerShadowed.Reason, "higher priority 80 > 50") {
			t.Fatalf("lower priority rule reason = %q", lowerShadowed.Reason)
		}

		negLit := candidatesByID["negative_literal_fail"]
		if negLit.Matched || !strings.Contains(negLit.Reason, "none: matched excluded literal") {
			t.Fatalf("negative literal rule = %#v", negLit)
		}

		negRe := candidatesByID["negative_regex_fail"]
		if negRe.Matched || !strings.Contains(negRe.Reason, "regex_none: matched excluded regular expression") {
			t.Fatalf("negative regex rule = %#v", negRe)
		}

		titleFail := candidatesByID["title_rule"]
		if titleFail.Matched || !strings.Contains(titleFail.Reason, "title_any") {
			t.Fatalf("title rule failed reason = %q", titleFail.Reason)
		}
	})

	t.Run("title-only match with ANSI in title", func(t *testing.T) {
		t.Parallel()
		snapshot := NormalizeSnapshot("ordinary screen", "\x1b[1mAPPROVAL REQUIRED: task 1\x1b[0m")
		inspection := manifest.Inspect(snapshot)
		if inspection.WinningRule != "title_rule" || inspection.Decision.Activity != registry.ActivityWaiting {
			t.Fatalf("title-only match = %#v", inspection)
		}
	})

	t.Run("top region boundary match", func(t *testing.T) {
		t.Parallel()
		screen := "HEADER_MARKER: started\nSecond line\nThird line\nFourth line"
		snapshot := NormalizeSnapshot(screen, "")
		inspection := manifest.Inspect(snapshot)
		if inspection.WinningRule != "top_region_rule" || inspection.Decision.Activity != registry.ActivityRunning {
			t.Fatalf("top region match = %#v", inspection)
		}
	})

	t.Run("Unicode and wrapped lines", func(t *testing.T) {
		t.Parallel()
		unicodeScreen := "› \n❯ \n✨ Action required: ALLOW and DENY\n"
		snapshot := NormalizeSnapshot(unicodeScreen, "")
		inspection := manifest.Inspect(snapshot)
		if inspection.WinningRule != "winner_rule" {
			t.Fatalf("Unicode and wrapped screen inspection = %#v", inspection)
		}
	})

	t.Run(">100 lines history bound", func(t *testing.T) {
		t.Parallel()
		lines := make([]string, 150)
		for i := range lines {
			lines[i] = "filler line"
		}
		// Put HEADER_MARKER at line 20 (which is in the first 50 lines, outside the last 100)
		lines[20] = "HEADER_MARKER"
		snapshot := NormalizeSnapshot(strings.Join(lines, "\n"), "")
		if len(snapshot.Lines) != 100 {
			t.Fatalf("len(snapshot.Lines) = %d, want 100", len(snapshot.Lines))
		}
		inspection := manifest.Inspect(snapshot)
		// top_region_rule checks top:2 of snapshot.Lines (which are lines 50..149 of original text), so HEADER_MARKER should not match
		if inspection.WinningRule != "" {
			t.Fatalf("marker outside 100 lines should not match, got = %#v", inspection.WinningRule)
		}
	})

	t.Run("empty screen results in no_rule_matched", func(t *testing.T) {
		t.Parallel()
		snapshot := NormalizeSnapshot("", "")
		inspection := manifest.Inspect(snapshot)
		if inspection.WinningRule != "" || inspection.Decision.Reason != "no_rule_matched" || inspection.Decision.Activity != registry.ActivityUnknown {
			t.Fatalf("empty screen inspection = %#v", inspection)
		}
	})
}

//nolint:cyclop,gocognit // subtest table cases verify each input boundary
func TestReadScreenInputBounds(t *testing.T) {
	t.Parallel()

	t.Run("read valid file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "screen.txt")
		if err := os.WriteFile(path, []byte("line 1\nline 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := ReadScreenInput(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if data != "line 1\nline 2\n" {
			t.Fatalf("data = %q, want line 1\\nline 2\\n", data)
		}
	})

	t.Run("read stdin", func(t *testing.T) {
		t.Parallel()
		stdin := strings.NewReader("stdin content\n")
		data, err := ReadScreenInput("-", stdin)
		if err != nil {
			t.Fatal(err)
		}
		if data != "stdin content\n" {
			t.Fatalf("data = %q", data)
		}
	})

	t.Run("read stdin unavailable", func(t *testing.T) {
		t.Parallel()
		_, err := ReadScreenInput("-", nil)
		if err == nil || !strings.Contains(err.Error(), "stdin is unavailable") {
			t.Fatalf("err = %v, want stdin is unavailable", err)
		}
	})

	t.Run("empty source path", func(t *testing.T) {
		t.Parallel()
		_, err := ReadScreenInput("", nil)
		if err == nil || !strings.Contains(err.Error(), "screen fixture path is required") {
			t.Fatalf("err = %v, want path is required", err)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		t.Parallel()
		_, err := ReadScreenInput(filepath.Join(t.TempDir(), "missing.txt"), nil)
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("oversized file exceeds MaxScreenBytes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "large.txt")
		largeData := make([]byte, MaxScreenBytes+10)
		if err := os.WriteFile(path, largeData, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ReadScreenInput(path, nil)
		if !errors.Is(err, ErrScreenTooLarge) {
			t.Fatalf("err = %v, want ErrScreenTooLarge", err)
		}
	})

	t.Run("oversized stdin exceeds MaxScreenBytes", func(t *testing.T) {
		t.Parallel()
		largeReader := bytes.NewReader(make([]byte, MaxScreenBytes+5))
		_, err := ReadScreenInput("-", largeReader)
		if !errors.Is(err, ErrScreenTooLarge) {
			t.Fatalf("err = %v, want ErrScreenTooLarge", err)
		}
	})
}

//nolint:cyclop,gocognit // subtest scenarios verify each explicit vs ambient error case
func TestLoadExplicitManifest(t *testing.T) {
	t.Parallel()

	validContent := []byte(`version=1
agent="codex"
[[rules]]
id="prompt"
state="idle"
priority=10
any=["READY"]
`)

	t.Run("valid manifest loads cleanly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "codex.toml")
		if err := os.WriteFile(path, validContent, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, err := LoadExplicitManifest(path, registry.HarnessCodex)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Source != path || manifest.Agent != "codex" || len(manifest.Rules) != 1 {
			t.Fatalf("manifest = %#v", manifest)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := LoadExplicitManifest(filepath.Join(t.TempDir(), "missing.toml"), registry.HarnessCodex)
		if err == nil {
			t.Fatal("expected error for missing manifest file")
		}
	})

	t.Run("malformed TOML returns error without fallback", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "malformed.toml")
		if err := os.WriteFile(path, []byte("invalid toml [["), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadExplicitManifest(path, registry.HarnessCodex)
		if err == nil || !strings.Contains(err.Error(), "parsing detection manifest") {
			t.Fatalf("expected parsing error, got: %v", err)
		}
	})

	t.Run("unknown fields rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "unknown_fields.toml")
		content := []byte(`version=1
agent="codex"
extra_field="rejected"
[[rules]]
id="r1"
state="idle"
priority=10
any=["ok"]
`)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadExplicitManifest(path, registry.HarnessCodex)
		if err == nil || !strings.Contains(err.Error(), "parsing TOML") {
			t.Fatalf("expected unknown field error, got: %v", err)
		}
	})

	t.Run("agent mismatch returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "wrong_agent.toml")
		if err := os.WriteFile(path, validContent, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadExplicitManifest(path, registry.HarnessClaude)
		if err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("expected agent mismatch error, got: %v", err)
		}
	})

	t.Run("invalid regex returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "bad_regex.toml")
		content := []byte(`version=1
agent="codex"
[[rules]]
id="r1"
state="idle"
priority=10
regex_any=["[unclosed"]
`)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadExplicitManifest(path, registry.HarnessCodex)
		if err == nil || !strings.Contains(err.Error(), "compiling rule") {
			t.Fatalf("expected regex error, got: %v", err)
		}
	})

	t.Run("oversized manifest exceeds 1 MiB", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "huge.toml")
		hugeData := make([]byte, maxManifestBytes+100)
		if err := os.WriteFile(path, hugeData, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadExplicitManifest(path, registry.HarnessCodex)
		if !errors.Is(err, errManifestTooLarge) {
			t.Fatalf("err = %v, want errManifestTooLarge", err)
		}
	})

	t.Run("contrast ambient override fallback vs explicit failure", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		overridePath := filepath.Join(configDir, "codex.toml")
		if err := os.WriteFile(overridePath, []byte("broken toml [["), 0o600); err != nil {
			t.Fatal(err)
		}

		// Ambient loader should succeed with warning and bundled fallback:
		ambientManifest, err := (Loader{ConfigDir: configDir}).Load(registry.HarnessCodex)
		if err != nil {
			t.Fatalf("ambient load unexpectedly failed: %v", err)
		}
		if ambientManifest.Warning == "" || !strings.Contains(ambientManifest.Warning, "ignoring invalid local override") {
			t.Fatalf("expected warning in ambient load, got: %q", ambientManifest.Warning)
		}
		if ambientManifest.Source != "bundled:codex" {
			t.Fatalf("expected bundled source, got: %q", ambientManifest.Source)
		}

		// Explicit loader MUST fail with error:
		_, explicitErr := LoadExplicitManifest(overridePath, registry.HarnessCodex)
		if explicitErr == nil {
			t.Fatal("explicit loader must fail on broken override file")
		}
	})
}

func TestDefaultConfigDir(t *testing.T) {
	tempDir := t.TempDir()
	xdgDir := filepath.Join(tempDir, "xdg_config")
	homeDir := filepath.Join(tempDir, "home")

	t.Run("XDG_CONFIG_HOME takes precedence", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", xdgDir)
		t.Setenv("HOME", homeDir)

		want := filepath.Join(xdgDir, "aht", "detection")
		if got := DefaultConfigDir(); got != want {
			t.Fatalf("DefaultConfigDir() = %q, want %q", got, want)
		}
	})

	t.Run("HOME/.config is used when XDG_CONFIG_HOME is unset", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", homeDir)

		want := filepath.Join(homeDir, ".config", "aht", "detection")
		if got := DefaultConfigDir(); got != want {
			t.Fatalf("DefaultConfigDir() = %q, want %q", got, want)
		}
	})
}
