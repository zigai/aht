package herdr_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/herdr"
)

func TestListPanesPreservesPartialResultsAndFatalErrorPrecedence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		malformed bool
	}{
		{name: "partial results"},
		{name: "fatal decode", malformed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var calls []string

			run := func(_ context.Context, env map[string]string, args ...string) (string, error) {
				call := env["HERDR_SESSION"] + ":" + strings.Join(args, " ")
				calls = append(calls, call)

				switch call {
				case ":session list --json":
					return `{"sessions":[{"name":"lost"},{"name":"work"},{"name":"later"}]}`, nil
				case "lost:api snapshot":
					return "", errUnexpectedCommand
				case "work:api snapshot":
					return `{"result":{"snapshot":{"panes":[{"pane_id":"p1"},{"pane_id":"p2"}]}}}`, nil
				case "work:pane process-info --pane p1":
					return "", errBinaryNotFound
				case "work:pane process-info --pane p2":
					if test.malformed {
						return "{", nil
					}

					return `{"result":{"process_info":{"shell_pid":12,"foreground_process_group_id":42}}}`, nil
				case "later:api snapshot":
					return `{"result":{"snapshot":{}}}`, nil
				default:
					t.Fatalf("unexpected command: %s", call)
					return "", errUnexpectedCommand
				}
			}

			panes, err := herdr.ListPanesWithOptions(t.Context(), herdr.ListOptions{Run: run})

			wantCalls := []string{
				":session list --json", "lost:api snapshot", "work:api snapshot",
				"work:pane process-info --pane p1", "work:pane process-info --pane p2",
			}
			if !test.malformed {
				wantCalls = append(wantCalls, "later:api snapshot")
			}

			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
			}

			if test.malformed {
				_, syntaxError := errors.AsType[*json.SyntaxError](err)
				if !syntaxError || panes != nil || errors.Is(err, errUnexpectedCommand) {
					t.Fatalf("fatal decode = %#v, %v", panes, err)
				}

				return
			}

			if !errors.Is(err, errUnexpectedCommand) || !errors.Is(err, errBinaryNotFound) {
				t.Fatalf("partial errors = %v", err)
			}

			wantError := "read herdr session \"lost\" snapshot: unexpected herdr command\n" +
				"read herdr pane process info: binary not found"
			if err.Error() != wantError {
				t.Fatalf("error order = %q, want %q", err, wantError)
			}

			if len(panes) != 2 || len(panes[0].Processes) != 0 ||
				panes[1].Location.PanePID != 12 || panes[1].Processes[0].ProcessGroupID != 42 {
				t.Fatalf("partial panes = %#v", panes)
			}
		})
	}
}
