package tmux

import (
	"context"
	"errors"
	"testing"
)

var errBinaryNotFound = errors.New("binary not found")

func TestListPanesSkipsWhenTmuxIsNotInstalled(t *testing.T) {
	driver := Driver{LookPath: func(string) (string, error) {
		return "", errBinaryNotFound
	}}
	panes, err := driver.ListPanes(context.Background())
	if err != nil || panes != nil {
		t.Fatalf("ListPanes() = %#v, %v", panes, err)
	}
}
