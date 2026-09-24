//go:build integration

package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/internal/testtmux"
)

func TestListPanesDiscoversRealNamedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("real tmux integration test")
	}
	name := fmt.Sprintf("aht-discovery-%d-%d", os.Getpid(), time.Now().UnixNano())
	testtmux.NewNamed(t, name, gotmux.NewSessionOptions{Name: "discovery", Program: gotmux.Exec("sleep", "30")})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		panes, err := ListPanesWithOptions(ctx, ListOptions{
			// Standard sockets must be discoverable even when process arguments
			// are unavailable or the caller is outside tmux.
			ServerProcesses: func(context.Context) ([]ServerProcess, error) { return nil, nil },
		})
		if err == nil {
			for _, pane := range panes {
				if pane.Tmux.SessionName != "discovery" || filepath.Base(pane.ServerIdentity) != name {
					continue
				}
				if pane.ServerIdentity == "-L:"+name || !filepath.IsAbs(pane.ServerIdentity) || pane.Tmux.ServerID != pane.ServerIdentity {
					t.Fatalf("named server identity was not canonical: %#v", pane)
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("named tmux server was not discovered: %v", err)
		case <-ticker.C:
		}
	}
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
		if pane.ServerIdentity == server.Socket && pane.Tmux.SessionName == "custom" {
			return
		}
	}
	t.Fatal("custom -S server was not discovered from process arguments")
}
