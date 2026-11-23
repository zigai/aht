package mux_test

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestBoundBottomLinesPreservesBlankRows(t *testing.T) {
	t.Parallel()

	input := "row 1\n\nrow 3\n\n"
	got := mux.BoundBottomLines(input, 3)

	want := []string{"", "row 3", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BoundBottomLines = %#v, want %#v", got, want)
	}
}

func TestProgramStatusActivity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		status mux.ProgramStatus
		want   registry.Activity
		detail registry.ActivityDetail
		known  bool
	}{
		{mux.ProgramStatus{State: "working"}, registry.ActivityRunning, "", true},
		{mux.ProgramStatus{State: "idle"}, registry.ActivityIdle, "", true},
		{mux.ProgramStatus{State: "done"}, registry.ActivityIdle, "", true},
		{mux.ProgramStatus{State: "error"}, registry.ActivityFailed, registry.ActivityDetailGeneral, true},
		{
			mux.ProgramStatus{State: "blocked", Kind: "permission"},
			registry.ActivityWaiting, registry.ActivityDetailPermission, true,
		},
		{
			mux.ProgramStatus{State: "blocked", Kind: "question"},
			registry.ActivityWaiting, registry.ActivityDetailQuestion, true,
		},
		{mux.ProgramStatus{State: "blocked", Kind: "auth"}, registry.ActivityWaiting, "", true},
		{mux.ProgramStatus{State: "working", Kind: "permission"}, registry.ActivityRunning, "", true},
		{mux.ProgramStatus{State: "clear"}, "", "", false},
		{mux.ProgramStatus{State: "paused"}, "", "", false},
	} {
		activity, detail, known := test.status.Activity()
		if activity != test.want || detail != test.detail || known != test.known {
			t.Errorf("%+v = %q, %q, %t; want %q, %q, %t",
				test.status, activity, detail, known, test.want, test.detail, test.known)
		}
	}
}
