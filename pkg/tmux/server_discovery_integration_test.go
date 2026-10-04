//go:build integration

package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/internal/testtmux"
)

func TestListPanesDiscoversRealNamedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("real tmux integration test")
	}
	name := fmt.Sprintf("aht-discovery-%d-%d", os.Getpid(), time.Now().UnixNano())
	server := testtmux.NewNamed(t, name, gotmux.NewSessionOptions{Name: "discovery", Program: gotmux.Exec("sleep", "30")})
	t.Setenv("PATH", filepath.Dir(testtmux.Executable(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		sockets, err := gotmux.DiscoverSockets()
		if err != nil {
			t.Fatal(err)
		}
		sockets = slices.DeleteFunc(sockets, func(socket string) bool {
			return filepath.Dir(socket) != filepath.Dir(server.Socket)
		})
		panes, err := ListPanesWithOptions(ctx, ListOptions{
			SocketPaths: sockets,
			// Standard sockets must be discoverable even when process arguments
			// are unavailable or the caller is outside tmux.
			ServerProcesses: func(context.Context) ([]ServerProcess, error) { return nil, nil },
		})
		if err == nil && namedDiscoveryPaneFound(t, panes, name) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("named tmux server was not discovered: %v", err)
		case <-ticker.C:
		}
	}
}

func namedDiscoveryPaneFound(t *testing.T, panes []Pane, name string) bool {
	t.Helper()
	for _, pane := range panes {
		if pane.Location.SessionName != "discovery" || filepath.Base(pane.Location.ServerID) != name {
			continue
		}
		if pane.Location.ServerID == "-L:"+name || !filepath.IsAbs(pane.Location.ServerID) {
			t.Fatalf("named server identity was not canonical: %#v", pane)
		}
		return true
	}
	return false
}

func TestListPanesDiscoversCustomSocketFallback(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("tmux process discovery is not supported on this platform")
	}
	server := testtmux.New(t, gotmux.NewSessionOptions{Name: "custom", Program: gotmux.Exec("sleep", "30")})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	panes, err := ListPanesWithOptions(ctx, ListOptions{SocketPaths: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, pane := range panes {
		if pane.Location.ServerID == server.Socket && pane.Location.SessionName == "custom" {
			return
		}
	}
	t.Fatal("custom -S server was not discovered from process arguments")
}
