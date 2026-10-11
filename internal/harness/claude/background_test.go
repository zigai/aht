package claude

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestStopActivityHoldsForDelegatedBackgroundWork(t *testing.T) {
	t.Parallel()
	at := localNoon(9, 26)
	for name, test := range map[string]struct {
		payload string
		want    registry.Activity
	}{
		"subagent":      {`{"background_tasks":[{"id":"a","type":"subagent","status":"running"}]}`, registry.ActivityRunning},
		"workflow":      {`{"background_tasks":[{"id":"a","type":"workflow","status":"running"}]}`, registry.ActivityRunning},
		"cloud session": {`{"background_tasks":[{"id":"a","type":"cloud session","status":"running"}]}`, registry.ActivityRunning},
		"MCP task":      {`{"background_tasks":[{"id":"a","type":"MCP task","status":"running"}]}`, registry.ActivityRunning},
		"subagent beside dev server": {
			`{"background_tasks":[{"id":"a","type":"shell","status":"running"},{"id":"b","type":"subagent","status":"running"}]}`,
			registry.ActivityRunning,
		},
		"shell":           {`{"background_tasks":[{"id":"a","type":"shell","status":"running"}]}`, registry.ActivityIdle},
		"monitor":         {`{"background_tasks":[{"id":"a","type":"monitor","status":"running"}]}`, registry.ActivityIdle},
		"teammate":        {`{"background_tasks":[{"id":"a","type":"teammate","status":"running"}]}`, registry.ActivityIdle},
		"unknown type":    {`{"background_tasks":[{"id":"a","type":"local_future","status":"running"}]}`, registry.ActivityIdle},
		"empty arrays":    {`{"background_tasks":[],"session_crons":[]}`, registry.ActivityIdle},
		"absent arrays":   {`{}`, registry.ActivityIdle},
		"malformed tasks": {`{"background_tasks":{"type":"subagent"},"session_crons":"*/5 * * * *"}`, registry.ActivityIdle},
		"self-paced wakeup in ten minutes": {
			`{"session_crons":[{"id":"c","schedule":"10 12 26 9 *","recurring":false}]}`,
			registry.ActivityRunning,
		},
		"fixed-interval loop": {
			`{"session_crons":[{"id":"c","schedule":"*/5 * * * *","recurring":true}]}`,
			registry.ActivityRunning,
		},
		"reminder tomorrow": {
			`{"session_crons":[{"id":"c","schedule":"0 9 27 9 *","recurring":false}]}`,
			registry.ActivityIdle,
		},
		"weekday morning schedule": {
			`{"session_crons":[{"id":"c","schedule":"3 9 * * 1-5","recurring":true}]}`,
			registry.ActivityIdle,
		},
		"unsupported schedule syntax": {
			`{"session_crons":[{"id":"c","schedule":"@hourly","recurring":true}]}`,
			registry.ActivityIdle,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}
			if got := New().PayloadActivity(harness.HookEventStop, registry.ActivityIdle, payload, at); got != test.want {
				t.Fatalf("PayloadActivity = %s, want %s", got, test.want)
			}
		})
	}
}

func TestPayloadActivityOnlyRefinesIdleStop(t *testing.T) {
	t.Parallel()
	at := localNoon(9, 26)
	payload := map[string]any{"background_tasks": []any{map[string]any{"id": "a", "type": "subagent"}}}
	for _, test := range []struct {
		event    string
		activity registry.Activity
	}{
		{"StopFailure", registry.ActivityFailed},
		{"PermissionDenied", registry.ActivityIdle},
		{"PostCompact", registry.ActivityIdle},
		{harness.HookEventStop, registry.ActivityFailed},
	} {
		if got := New().PayloadActivity(test.event, test.activity, payload, at); got != test.activity {
			t.Fatalf("%s %s refined to %s", test.event, test.activity, got)
		}
	}
}

func TestCronScheduleFollowsClaudeCodeSyntax(t *testing.T) {
	t.Parallel()
	saturdayNoon := localNoon(9, 26)
	sundayNoon := localNoon(9, 27)
	firstOfMonth := localNoon(10, 1)
	for _, test := range []struct {
		expression string
		at         time.Time
		want       bool
	}{
		{"0 12 * * *", saturdayNoon, true},
		{"0 12 * * 6", saturdayNoon, true},
		{"0 12 * * 0", sundayNoon, true},
		{"0 12 * * 7", sundayNoon, true},
		{"0 12 * * 1-5", saturdayNoon, false},
		{"0 8-16/4 * * *", saturdayNoon, true},
		{"0 8-16/3 * * *", saturdayNoon, false},
		{"0,30 12 * * *", saturdayNoon, true},
		{"*/15 * * * *", saturdayNoon, true},
		// Restricted day-of-month and day-of-week match when either matches.
		{"0 12 1 * 6", saturdayNoon, true},
		{"0 12 1 * 6", firstOfMonth, true},
		{"0 12 2 * 5", saturdayNoon, false},
		// A day field starting with "*" is unrestricted, so both must match.
		{"0 12 */2 * 6", saturdayNoon, false},
	} {
		schedule, ok := parseCronSchedule(test.expression)
		if !ok {
			t.Fatalf("parseCronSchedule(%q) rejected supported syntax", test.expression)
		}
		if got := schedule.matches(test.at); got != test.want {
			t.Fatalf("%q matches %s = %t, want %t", test.expression, test.at, got, test.want)
		}
	}
}

func TestCronScheduleRejectsUnsupportedSyntax(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{
		"",
		"* * * *",
		"* * * * * *",
		"0 9 * * MON",
		"0 9 * JAN *",
		"0 9 L * *",
		"0 9 ? * *",
		"60 * * * *",
		"0 24 * * *",
		"0 9 0 * *",
		"0 9 * 13 *",
		"0 9 * * 8",
		"5-1 * * * *",
		"*/0 * * * *",
		"5/15 * * * *",
		"0,,5 * * * *",
	} {
		if _, ok := parseCronSchedule(expression); ok {
			t.Fatalf("parseCronSchedule(%q) accepted unsupported syntax", expression)
		}
	}
}

func localNoon(month time.Month, day int) time.Time {
	//nolint:gosmopolitan // Claude Code evaluates session crons in the local timezone.
	return time.Date(2026, month, day, 12, 0, 0, 0, time.Local)
}
