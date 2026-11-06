//go:build linux

package processinfo

import (
	"errors"
	"strings"
	"testing"
)

func TestParseLinuxStatValidationPrecedence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		header string
		fields []string
		want   error
	}{
		{
			"delimiter before pid", "bad ", nil, errMissingCommandDelimiter,
		},
		{
			"pid before payload", "bad (agent) ", []string{"bad"}, errInvalidProcessID,
		},
		{
			"length before state",
			"123 (agent) ",
			[]string{"bad"},
			errTruncatedStatRecord,
		},
		{
			"state before parent",
			"123 (agent) ",
			invalidStartStatFields("bad", "bad", "bad", "bad"),
			errInvalidProcessState,
		},
		{
			"parent before group",
			"123 (agent) ",
			invalidStartStatFields("S", "-1", "bad", "bad"),
			errInvalidParentProcessID,
		},
		{
			"group before foreground",
			"123 (agent) ",
			invalidStartStatFields("S", "1", "-1", "bad"),
			errInvalidProcessGroupID,
		},
		{
			"foreground before start",
			"123 (agent) ",
			invalidStartStatFields("S", "1", "2", "-2"),
			errInvalidForegroundGroup,
		},
		{
			"invalid start",
			"123 (agent) ",
			invalidStartStatFields("S", "1", "2", "-1"),
			errInvalidProcessStartTime,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseLinuxStat(test.header + strings.Join(test.fields, " "))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func invalidStartStatFields(
	state string,
	parent string,
	group string,
	foreground string,
) []string {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}

	fields[0], fields[1], fields[2], fields[5], fields[19] = state, parent, group, foreground, "bad"

	return fields
}
