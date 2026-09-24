package observer

import (
	"testing"

	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/registry"
)

func TestResolveHarnessUsesScopedAgentHint(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{Executable: "/usr/bin/fence", AgentHint: "claude", Args: []string{"fence", "--", "node"}}
	harness, ok := resolveHarness(process)
	if !ok || harness != registry.Harness("claude") {
		t.Fatalf("resolveHarness = %q, %v; want Claude from hint", harness, ok)
	}
}

func TestResolveHarnessIgnoresOmpInternalWorkers(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{
		Executable: "/home/test/.local/bin/omp",
		Args:       []string{"/home/test/.local/bin/omp", "__omp_worker_js_eval_process"},
	}
	harness, ok := resolveHarness(process)
	if ok || harness != "" {
		t.Fatalf("resolveHarness = %q, %v; want internal OMP worker ignored", harness, ok)
	}
}

func TestResolveHarnessKeepsOmpHeadlessSessions(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{
		Executable: "/home/test/.local/bin/omp",
		Args:       []string{"/home/test/.local/bin/omp", "--print", "check this repository"},
	}
	harness, ok := resolveHarness(process)
	if !ok || harness != registry.Harness("omp") {
		t.Fatalf("resolveHarness = %q, %v; want headless OMP session", harness, ok)
	}
}

func TestResolveHarnessScansKnownWrappers(t *testing.T) {
	t.Parallel()
	for _, wrapper := range []string{"env", "fence", "bwrap", "bubblewrap", "mise", "nix-shell", "nix", "direnv"} {
		t.Run(wrapper, func(t *testing.T) {
			t.Parallel()
			process := processinfo.Process{Executable: "/usr/bin/" + wrapper, Args: []string{wrapper, "--wrapper-option", "value", "--", "/usr/bin/codex"}}
			harness, ok := resolveHarness(process)
			if !ok || harness != registry.Harness("codex") {
				t.Fatalf("resolveHarness = %q, %v; want Codex behind %s", harness, ok, wrapper)
			}
		})
	}
}

func TestResolveHarnessIgnoresElectronHelperSubprocesses(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"--type=zygote", "--type=utility", "--type=gpu-process", "--type=renderer"} {
		process := processinfo.Process{
			Executable: "/home/test/.local/bin/agy",
			Args:       []string{"/home/test/.local/bin/agy", arg},
		}
		harness, ok := resolveHarness(process)
		if ok || harness != "" {
			t.Fatalf("resolveHarness with %s = %q, %v; want ignored helper", arg, harness, ok)
		}
	}
}

func TestResolveHarnessIgnoresTestFixtures(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{
		Executable: "/tmp/aht-systest-12345/codex",
		Args:       []string{"/tmp/aht-systest-12345/codex", "--store", "/tmp/aht-systest-12345/state.json"},
	}
	harness, ok := resolveHarness(process)
	if ok || harness != "" {
		t.Fatalf("resolveHarness = %q, %v; want ignored test fixture", harness, ok)
	}
}

func TestResolveHarnessIgnoresAhtTrackerLoop(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{
		Executable: "/home/test/bin/codex",
		Args:       []string{"codex", "manage", "tracker", "run", "--quiet"},
	}
	harness, ok := resolveHarness(process)
	if ok || harness != "" {
		t.Fatalf("resolveHarness = %q, %v; want ignored tracker runner", harness, ok)
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
