package claude

import (
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

// wakeupHorizon bounds how far ahead a session wakeup keeps a stopped turn
// running. Self-paced loops reschedule themselves at most one hour ahead.
const wakeupHorizon = time.Hour

const (
	cronFieldCount = 5
	// cronSundayAlias is the day-of-week value Claude Code accepts for Sunday
	// alongside 0.
	cronSundayAlias = 7
)

// heldTaskTypes lists background task types that report back to the main
// session when they finish. Shell, monitor, and teammate tasks can outlive the
// work that started them (dev servers, log watchers, idle teammates), so they
// never keep a stopped turn running.
var heldTaskTypes = map[string]bool{
	"subagent":      true,
	"workflow":      true,
	"cloud session": true,
	"MCP task":      true,
}

// cronFieldBounds holds the minute, hour, day-of-month, month, and day-of-week
// ranges accepted by Claude Code's CronCreate. Day-of-week 7 is Sunday.
var cronFieldBounds = [cronFieldCount][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

// cronSchedule is a parsed 5-field cron expression evaluated in local time,
// matching Claude Code's scheduler. Each field is a bitset of allowed values.
type cronSchedule struct {
	minutes            uint64
	hours              uint64
	days               uint64
	months             uint64
	weekdays           uint64
	daysRestricted     bool
	weekdaysRestricted bool
}

// PayloadActivity keeps a stopped turn running while Claude Code reports
// delegated background work or a due session wakeup that will resume the turn
// without user input. Claude Code documents the Stop payload's
// background_tasks and session_crons arrays for exactly this distinction.
func (claudeHarness) PayloadActivity(
	event string,
	activity registry.Activity,
	payload map[string]any,
	at time.Time,
) registry.Activity {
	if event != harness.HookEventStop || activity != registry.ActivityIdle {
		return activity
	}
	if holdsBackgroundTask(payload) || holdsSessionWakeup(payload, at) {
		return registry.ActivityRunning
	}
	return activity
}

func holdsBackgroundTask(payload map[string]any) bool {
	tasks, _ := payload["background_tasks"].([]any)
	for _, value := range tasks {
		task, ok := value.(map[string]any)
		if ok && heldTaskTypes[harness.PayloadString(task, "type")] {
			return true
		}
	}
	return false
}

func holdsSessionWakeup(payload map[string]any, at time.Time) bool {
	crons, _ := payload["session_crons"].([]any)
	for _, value := range crons {
		cron, ok := value.(map[string]any)
		if !ok {
			continue
		}
		schedule, ok := parseCronSchedule(harness.PayloadString(cron, "schedule"))
		//nolint:gosmopolitan // Claude Code evaluates session crons in the local timezone.
		if ok && schedule.firesWithin(at.Local(), wakeupHorizon) {
			return true
		}
	}
	return false
}

// parseCronSchedule accepts the syntax Claude Code documents for CronCreate:
// wildcards, single values, steps, ranges, and comma-separated lists.
// Unsupported syntax reports false so the turn is treated as finished.
func parseCronSchedule(expression string) (cronSchedule, bool) {
	var unsupported cronSchedule
	fields := strings.Fields(expression)
	if len(fields) != cronFieldCount {
		return unsupported, false
	}
	var sets [cronFieldCount]uint64
	for index, field := range fields {
		set, ok := parseCronField(field, cronFieldBounds[index][0], cronFieldBounds[index][1])
		if !ok {
			return unsupported, false
		}
		sets[index] = set
	}
	weekdays := sets[4]
	if weekdays&(1<<cronSundayAlias) != 0 {
		weekdays |= 1
	}
	return cronSchedule{
		minutes:  sets[0],
		hours:    sets[1],
		days:     sets[2],
		months:   sets[3],
		weekdays: weekdays,
		// Vixie cron treats a day field that starts with "*" as unrestricted.
		daysRestricted:     !strings.HasPrefix(fields[2], "*"),
		weekdaysRestricted: !strings.HasPrefix(fields[4], "*"),
	}, true
}

func parseCronField(field string, low int, high int) (uint64, bool) {
	var set uint64
	for part := range strings.SplitSeq(field, ",") {
		first, last, step, ok := parseCronPart(part, low, high)
		if !ok {
			return 0, false
		}
		for value := first; value <= last; value += step {
			set |= 1 << value
		}
	}
	return set, true
}

// parseCronPart returns the first value, last value, and step of one
// comma-separated cron list element.
func parseCronPart(part string, low int, high int) (int, int, int, bool) {
	base, stepText, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		parsed, err := strconv.Atoi(stepText)
		if err != nil || parsed <= 0 {
			return 0, 0, 0, false
		}
		step = parsed
	}
	if base == "*" {
		return low, high, step, true
	}
	if from, to, isRange := strings.Cut(base, "-"); isRange {
		first, firstOK := cronValue(from, low, high)
		last, lastOK := cronValue(to, low, high)
		return first, last, step, firstOK && lastOK && first <= last
	}
	value, ok := cronValue(base, low, high)
	return value, value, step, ok && !hasStep
}

func cronValue(text string, low int, high int) (int, bool) {
	value, err := strconv.Atoi(text)
	return value, err == nil && value >= low && value <= high
}

// firesWithin reports whether the schedule has a fire time from the start of
// the current minute through horizon.
func (schedule cronSchedule) firesWithin(from time.Time, horizon time.Duration) bool {
	end := from.Add(horizon)
	for minute := from.Truncate(time.Minute); !minute.After(end); minute = minute.Add(time.Minute) {
		if schedule.matches(minute) {
			return true
		}
	}
	return false
}

func (schedule cronSchedule) matches(at time.Time) bool {
	if !hasCronBit(schedule.minutes, at.Minute()) ||
		!hasCronBit(schedule.hours, at.Hour()) ||
		!hasCronBit(schedule.months, int(at.Month())) {
		return false
	}
	day := hasCronBit(schedule.days, at.Day())
	weekday := hasCronBit(schedule.weekdays, int(at.Weekday()))
	if schedule.daysRestricted && schedule.weekdaysRestricted {
		return day || weekday
	}
	return day && weekday
}

func hasCronBit(set uint64, value int) bool {
	return set&(1<<value) != 0
}
