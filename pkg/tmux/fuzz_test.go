package tmux

import "testing"

func FuzzParseCurrent(f *testing.F) {
	f.Add("$1\twork\t@2\t3\tapi\t%4\t1\t/home/me/project\t1234\t/dev/pts/5\t/dev/pts/1\n")
	f.Add("tmuxctx:\\$1 tmuxctx:work tmuxctx:@2 tmuxctx:3 tmuxctx:api tmuxctx:%4 tmuxctx:1 tmuxctx:'/tmp/with space' tmuxctx:1234 tmuxctx:/dev/pts/5 tmuxctx:/dev/pts/1\n")
	f.Add("tmuxctx:'unterminated")

	f.Fuzz(func(t *testing.T, output string) {
		ctx, err := ParseCurrent(output)
		if err != nil {
			return
		}
		if !ctx.Inside {
			t.Fatal("successfully parsed context is not marked inside tmux")
		}
		if ctx.PanePID < 0 {
			t.Fatalf("pane PID = %d, want non-negative", ctx.PanePID)
		}
	})
}

func FuzzParseListPanes(f *testing.F) {
	f.Add("$1\twork\t@2\t3\tapi\t%4\t1\t/home/me/project\t1234\t/dev/pts/5\t/tmp/tmux-1000/default\n")
	f.Add("tmuxctx:\\$1 tmuxctx:work tmuxctx:@2 tmuxctx:3 tmuxctx:api tmuxctx:%4 tmuxctx:1 tmuxctx:'/tmp/with space' tmuxctx:1234 tmuxctx:/dev/pts/5 tmuxctx:/tmp/tmux-1000/default\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, output string) {
		panes, err := ParseListPanes(output)
		if err != nil {
			return
		}
		for _, pane := range panes {
			if !pane.Tmux.Inside {
				t.Fatal("successfully parsed pane is not marked inside tmux")
			}
			if pane.PanePID != pane.Tmux.PanePID || pane.PaneTTY != pane.Tmux.PaneTTY {
				t.Fatalf("pane process fields disagree with tmux context: %#v", pane)
			}
		}
	})
}
