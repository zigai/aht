package observer

import (
	"testing"

	"github.com/zigai/aht/v2/internal/processinfo"
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
			name: "scoped agent hint",
			process: processinfo.Process{
				Executable: "/usr/bin/fence",
				AgentHint:  "claude",
				Args: []string{
					"fence",
					"--",
					"node",
				},
			},
			want: "claude",
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
				Executable: "/tmp/aht-systest-12345/codex",
				Args:       []string{"/tmp/aht-systest-12345/codex", "--store", "/tmp/aht-systest-12345/state.json"},
			},
		},
		{
			name: "aht tracker loop",
			process: processinfo.Process{
				Executable: "/home/test/bin/codex",
				Args:       []string{"codex", "manage", "tracker", "run", "--quiet"},
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

func TestResolveHarnessScansKnownWrappers(t *testing.T) {
	t.Parallel()

	for _, wrapper := range []string{"env", "fence", "bwrap", "bubblewrap", "mise", "nix-shell", "nix", "direnv"} {
		t.Run(wrapper, func(t *testing.T) {
			t.Parallel()

			process := processinfo.Process{
				Executable: "/usr/bin/" + wrapper,
				Args:       []string{wrapper, "--wrapper-option", "value", "--", "/usr/bin/codex"},
			}

			harness, ok := resolveHarness(process)
			if !ok || harness != registry.Harness("codex") {
				t.Fatalf("resolveHarness = %q, %v; want Codex behind %s", harness, ok, wrapper)
			}
		})
	}
}

func TestHasAncestorHarness(t *testing.T) {
	t.Parallel()

	processes := map[int]processinfo.Process{
		10: {PID: 10, PPID: 1},
		20: {PID: 20, PPID: 10},
		30: {PID: 30, PPID: 20},
		40: {PID: 40, PPID: 1},
	}

	harnessByPID := map[int]registry.Harness{
		10: registry.Harness("claude"),
		20: registry.Harness("claude"),
		30: registry.Harness("claude"),
		40: registry.Harness("codex"),
	}
	if hasAncestorHarness(10, registry.Harness("claude"), processes, harnessByPID) {
		t.Fatal("PID 10 unexpectedly has ancestor with same harness")
	}

	if !hasAncestorHarness(20, registry.Harness("claude"), processes, harnessByPID) {
		t.Fatal("PID 20 should have PID 10 as ancestor with same harness")
	}

	if !hasAncestorHarness(30, registry.Harness("claude"), processes, harnessByPID) {
		t.Fatal("PID 30 should have ancestor with same harness")
	}

	if hasAncestorHarness(40, registry.Harness("codex"), processes, harnessByPID) {
		t.Fatal("PID 40 should not have ancestor with same harness")
	}
}
