package mux_test

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/pkg/mux"
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
