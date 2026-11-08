package observer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestResolveHarnessIdentifiesSessionProcesses(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		process processinfo.Process
		want    registry.Harness
	}{
		{
			name: "session with tracker words",
			process: processinfo.Process{
				Executable: "/usr/bin/codex",
				Args:       []string{"codex", "manage", "tracker", "run"},
			},
			want: "codex",
		},
		{
			name: "session with a type argument",
			process: processinfo.Process{
				Executable: "/usr/bin/codex",
				Args:       []string{"codex", "--type=utility"},
			},
			want: "codex",
		},
		{
			name: "session in a directory named like a fixture",
			process: processinfo.Process{
				Executable: "/work/aht-systest-example/codex",
				CWD:        "/work/aht-systest-example",
			},
			want: "codex",
		},
		{
			name: "omp headless session",
			process: processinfo.Process{
				Executable: "/home/test/.local/bin/omp",
				Args:       []string{"/home/test/.local/bin/omp", "--print", "check this repository"},
			},
			want: "omp",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness, ok := resolveHarness(test.process)
			if !ok || harness != test.want {
				t.Fatalf("resolveHarness = %q, %v; want %q", harness, ok, test.want)
			}
		})
	}
}

func TestResolveHarnessIgnoresNonSessionProcesses(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		process processinfo.Process
	}{
		{
			name: "omp internal worker",
			process: processinfo.Process{
				Executable: "/home/test/.local/bin/omp",
				Args:       []string{"/home/test/.local/bin/omp", "__omp_worker_js_eval_process"},
			},
		},
		{
			name: "test fixture",
			process: processinfo.Process{
				Executable: "/work/fixtures/codex",
				Env:        map[string]string{processinfo.ChildRoleEnv: "fixture"},
				Args:       []string{"/tmp/aht-systest-12345/codex", "--store", "/tmp/aht-systest-12345/state.json"},
			},
		},
		{
			name: "aht tracker loop",
			process: processinfo.Process{
				Executable: "/home/test/bin/codex",
				Args:       []string{"codex", "manage", "tracker", "run", "--quiet"},
				Env:        map[string]string{processinfo.ChildRoleEnv: "tracker"},
			},
		},
		{
			name: "electron helper --type=zygote",
			process: processinfo.Process{
				Executable: "/home/test/.local/bin/agy",
				Args:       []string{"/home/test/.local/bin/agy", "--type=zygote"},
			},
		},
		{
			name: "electron helper --type=utility",
			process: processinfo.Process{
				Executable: "/home/test/.local/bin/agy",
				Args:       []string{"/home/test/.local/bin/agy", "--type=utility"},
			},
		},
		{
			name: "electron helper --type=gpu-process",
			process: processinfo.Process{
				Executable: "/home/test/.local/bin/agy",
				Args:       []string{"/home/test/.local/bin/agy", "--type=gpu-process"},
			},
		},
		{
			name: "electron helper --type=renderer",
			process: processinfo.Process{
				Executable: "/home/test/.local/bin/agy",
				Args:       []string{"/home/test/.local/bin/agy", "--type=renderer"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness, ok := resolveHarness(test.process)
			if ok || harness != "" {
				t.Fatalf("resolveHarness = %q, %v; want ignored", harness, ok)
			}
		})
	}
}

func TestObserverFindsHarnessBehindWrapper(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{
		PID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/env",
		Args: []string{"env", "AGENT_MODE=1", "--", "/usr/bin/codex"},
	}
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})

	watcher := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 || sessions[0].Harness != "codex" ||
		sessions[0].Process == nil ||
		sessions[0].Process.PID != process.PID {
		t.Fatalf("wrapper session=%#v err=%v", sessions, err)
	}
}
