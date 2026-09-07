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

	"github.com/zigai/aht/internal/testtmux"
)

func TestListPanesDiscoversRealNamedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("real tmux integration test")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("tmux process discovery is not supported on this platform")
	}
	name := fmt.Sprintf("aht-discovery-%d-%d", os.Getpid(), time.Now().UnixNano())
	testtmux.NewNamed(t, name, "-s", "discovery", "sleep", "30")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		panes, err := ListPanes(ctx)
		if err == nil {
			for _, pane := range panes {
				if pane.Tmux.SessionName != "discovery" || filepath.Base(pane.ServerIdentity) != name {
					continue
				}
				if pane.ServerIdentity == "-L:"+name || !filepath.IsAbs(pane.ServerIdentity) || pane.Tmux.ServerSocket != pane.ServerIdentity {
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
