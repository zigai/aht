package herdr_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/herdr"
)

func TestListPanesPreservesPartialResults(t *testing.T) {
	t.Parallel()

	run := func(_ context.Context, env map[string]string, args ...string) (string, error) {
		switch env["HERDR_SESSION"] + ":" + strings.Join(args, " ") {
		case ":session list --json":
			return `["lost","broken","work","later"]`, nil
		case "lost:api snapshot":
			return "", errUnexpectedCommand
		case "broken:api snapshot", "work:pane process-info --pane p1":
			return "{", nil
		case "work:api snapshot":
			return `{"result":{"snapshot":{"panes":[{"pane_id":"p1"},{"pane_id":"p2"}]}}}`, nil
		case "work:pane process-info --pane p2":
			return `{"result":{"process_info":{"shell_pid":12,"foreground_process_group_id":42}}}`, nil
		case "later:api snapshot":
			return `{"result":{"snapshot":{"panes":[{"pane_id":"p3"}]}}}`, nil
		case "later:pane process-info --pane p3":
			return "", errBinaryNotFound
		default:
			t.Fatalf("unexpected command: %v", args)
			return "", errUnexpectedCommand
		}
	}

	panes, err := herdr.ListPanesWithOptions(t.Context(), herdr.ListOptions{Run: run})
	if _, syntaxError := errors.AsType[*json.SyntaxError](err); !syntaxError ||
		!errors.Is(err, errUnexpectedCommand) || !errors.Is(err, errBinaryNotFound) ||
		!strings.Contains(err.Error(), "parse herdr snapshot") ||
		!strings.Contains(err.Error(), "parse herdr process info") {
		t.Fatalf("partial errors = %v", err)
	}

	if len(panes) != 3 || panes[0].Location.PaneID != "p1" || len(panes[0].Processes) != 0 ||
		panes[1].Location.PanePID != 12 || panes[2].Location.PaneID != "p3" {
		t.Fatalf("partial panes = %#v", panes)
	}
}

func TestListPanesStopsOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	run := func(_ context.Context, env map[string]string, args ...string) (string, error) {
		switch env["HERDR_SESSION"] + ":" + strings.Join(args, " ") {
		case ":session list --json":
			return `["work","later"]`, nil
		case "work:api snapshot":
			return `{"result":{"snapshot":{"panes":[{"pane_id":"p1"}]}}}`, nil
		case "work:pane process-info --pane p1":
			cancel()
			return "", errUnexpectedCommand
		default:
			t.Fatalf("unexpected command after cancellation: %v", args)
			return "", errUnexpectedCommand
		}
	}

	panes, err := herdr.ListPanesWithOptions(ctx, herdr.ListOptions{Run: run})
	if panes != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list = %#v, %v", panes, err)
	}
}
