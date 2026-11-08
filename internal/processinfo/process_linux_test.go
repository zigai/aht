//go:build linux

package processinfo

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestParseLinuxStatHandlesParentheses(t *testing.T) {
	fields := []string{"S", "41", "42"}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}

	fields[5] = "42"
	fields = append(fields, "987654")

	got, err := parseLinuxStat("123 (agent ) worker) " + strings.Join(fields, " "))
	if err != nil {
		t.Fatalf("parseLinuxStat returned error: %v", err)
	}

	if got.PID != 123 || got.PPID != 41 || got.ProcessGroupID != 42 || !got.Foreground ||
		got.StartIdentity != "987654" {
		t.Fatalf("parsed process = %#v", got)
	}
}

func TestLinuxEnvironmentKeepsFirstRequestedValues(t *testing.T) {
	t.Parallel()

	got := parseEnvironment(
		[]byte(
			"PATH=/usr/bin\x00NOT_PANE=wrong\x00PANE= 7 \x00PANE=8\x00EMPTY=\x00EMPTY=late"+
				"\x00SOCKET=/tmp/socket=name\x00OTHER=value\x00",
		),
		[]string{"PANE", "EMPTY", "SOCKET", "MISSING"},
	)

	want := map[string]string{"PANE": "7", "EMPTY": "", "SOCKET": "/tmp/socket=name"}
	if !maps.Equal(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
}

func TestReadLinuxProcessReadsEnvironmentOnlyWhenRequested(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "stat"),
		[]byte("123 (codex) S 1 42 42 0 0 0 0 0 0 0 0 0 0 0 0 0 0 987654 0 0"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(dir, "environ"),
		[]byte("ZELLIJ_SESSION_NAME=work\x00ZELLIJ_PANE_ID=7\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var reader linuxProcessReader

	process, err := reader.read(dir, 123, "boot", []string{"ZELLIJ_PANE_ID"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if want := map[string]string{"ZELLIJ_PANE_ID": "7"}; !maps.Equal(process.Env, want) {
		t.Fatalf("environment = %#v, want %#v", process.Env, want)
	}

	process, err = reader.read(dir, 123, "boot", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if process.Env != nil {
		t.Fatalf("environment read without requested keys: %#v", process.Env)
	}
}

func TestLinuxEnvironmentSelectionUsesFreshArguments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stat"),
		[]byte("123 (runner) S 1 42 42 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 987654"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte("PANE=7\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	includeEnv := func(process Process) bool {
		return len(process.Args) > 1 && process.Args[1] == "agent"
	}

	var reader linuxProcessReader

	for _, test := range []struct {
		args string
		env  map[string]string
	}{
		{"runner\x00worker\x00", nil},
		{"runner\x00agent\x00", map[string]string{"PANE": "7"}},
		{"runner\x00worker\x00", nil},
	} {
		if err := os.WriteFile(
			filepath.Join(dir, "cmdline"),
			[]byte(test.args),
			0o600,
		); err != nil {
			t.Fatal(err)
		}

		process, err := reader.read(dir, 123, "boot", []string{"PANE"}, includeEnv)
		if err != nil {
			t.Fatal(err)
		}

		if !maps.Equal(process.Env, test.env) {
			t.Fatalf("args %q environment = %#v, want %#v", test.args, process.Env, test.env)
		}

		if process.PID != 123 || process.StartIdentity != "boot:987654" {
			t.Fatalf("process identity changed: %#v", process)
		}
	}
}

func TestLinuxEnvironmentReaderKeepsSnapshotsIndependent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "environ")
	keys := []string{"PANE"}

	var reader linuxProcessReader

	if err := os.WriteFile(path, []byte("PANE=41\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	first := reader.readEnvironment(path, keys)
	if err := os.WriteFile(path, []byte("PANE=42\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	second := reader.readEnvironment(path, keys)
	if !maps.Equal(first, map[string]string{"PANE": "41"}) ||
		!maps.Equal(second, map[string]string{"PANE": "42"}) {
		t.Fatalf("environment snapshots = %#v, %#v", first, second)
	}

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if empty := reader.readEnvironment(path, keys); empty != nil {
		t.Fatalf("empty environment retained values: %#v", empty)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if missing := reader.readEnvironment(path, keys); missing != nil {
		t.Fatalf("missing environment retained values: %#v", missing)
	}
}

func TestParseLinuxStatRejectsMalformedRecord(t *testing.T) {
	if _, err := parseLinuxStat("123 (agent) S 1"); err == nil {
		t.Fatal("expected malformed stat record error")
	}
}

func TestListCurrentUser(t *testing.T) {
	processes, err := List(t.Context())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	var current *Process

	for i := range processes {
		if processes[i].PID == os.Getpid() {
			current = &processes[i]
			break
		}
	}

	if current == nil {
		t.Fatal("List did not include the current process")
	}

	if current.StartIdentity == "" || !strings.Contains(current.StartIdentity, ":") {
		t.Fatalf("current process identity = %q", current.StartIdentity)
	}
}

func FuzzParseLinuxStat(f *testing.F) {
	f.Add("123 (agent) S 1 123 0 0 0 123 0 0 0 0 0 0 0 0 0 0 0 0 987654")
	f.Add("123 (agent ) worker) S 1 123 0 0 0 123 0 0 0 0 0 0 0 0 0 0 0 0 987654")
	f.Add("not a stat record")

	f.Fuzz(func(t *testing.T, stat string) {
		process, err := parseLinuxStat(stat)
		if err != nil {
			return
		}

		if process.PID <= 0 || process.PPID < 0 || process.ProcessGroupID < 0 {
			t.Fatalf("invalid process fields accepted: %#v", process)
		}

		if _, err := strconv.ParseUint(process.StartIdentity, 10, 64); err != nil {
			t.Fatalf("invalid start identity accepted: %q", process.StartIdentity)
		}
	})
}

func TestScannerRefreshesExecAndPlacement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := writeScannerProcess(t, root, 100, "worker", "10", []string{"worker", "job", "agent"})

	var scanner Scanner
	defer scanner.Close()

	include := func(process Process) bool {
		return (len(process.Args) > 1 && process.Args[1] == "agent") ||
			(process.Executable == "/usr/bin/interpreter" && slices.Contains(process.Args, "agent"))
	}
	allArgs := func(process Process) bool {
		return process.Executable == "/usr/bin/interpreter" || include(process)
	}

	first, err := scanner.scan(t.Context(), root, "boot", []string{"PANE"}, include, allArgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(first) != 1 || !slices.Equal(first[0].Args, []string{"worker", "job"}) ||
		first[0].Env != nil || first[0].CWD != "" {
		t.Fatalf("unrelated process inventory = %#v", first)
	}

	replaceScannerLink(t, filepath.Join(dir, "exe"), "/usr/bin/interpreter")

	if err := os.WriteFile(
		filepath.Join(dir, "cmdline"),
		[]byte("interpreter\x00--native\x00agent\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	second, err := scanner.scan(t.Context(), root, "boot", []string{"PANE"}, include, allArgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(second) != 1 || second[0].StartIdentity != "boot:10" ||
		!maps.Equal(second[0].Env, map[string]string{"PANE": "7"}) ||
		second[0].CWD != "/project/first" || second[0].TTY != "/dev/pts/7" {
		t.Fatalf("same-PID agent exec = %#v", second)
	}

	replaceScannerLink(t, filepath.Join(dir, "cwd"), "/project/second")
	replaceScannerLink(t, filepath.Join(dir, "fd", "0"), "/dev/pts/8")

	if err := os.WriteFile(filepath.Join(dir, "environ"), []byte("PANE=8\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	third, err := scanner.scan(t.Context(), root, "boot", []string{"PANE"}, include, allArgs)
	if err != nil {
		t.Fatal(err)
	}

	if len(third) != 1 || third[0].CWD != "/project/second" || third[0].TTY != "/dev/pts/8" ||
		!maps.Equal(third[0].Env, map[string]string{"PANE": "8"}) {
		t.Fatalf("agent placement update = %#v", third)
	}

	if !slices.Equal(first[0].Args, []string{"worker", "job"}) ||
		!maps.Equal(second[0].Env, map[string]string{"PANE": "7"}) {
		t.Fatalf("previous snapshots changed: %#v, %#v", first, second)
	}
}

func TestScannerReplacesReusedPID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := writeScannerProcess(t, root, 100, "first", "10", []string{"first"})

	var scanner Scanner
	defer scanner.Close()

	first, err := scanner.scan(t.Context(), root, "boot", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	writeScannerProcess(t, root, 100, "second", "20", []string{"second", "changed"})

	second, err := scanner.scan(t.Context(), root, "boot", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(first) != 1 || len(second) != 1 || first[0].StartIdentity != "boot:10" ||
		second[0].StartIdentity != "boot:20" || second[0].Name != "second" ||
		!slices.Equal(second[0].Args, []string{"second", "changed"}) {
		t.Fatalf("PID reuse inventories = %#v, %#v", first, second)
	}
}

func TestScannerReadsLongArgumentsAndEmptyPrefixes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	long := strings.Repeat("x", 9000)
	writeScannerProcess(t, root, 100, "interpreter", "10", []string{"interpreter", long, "agent"})
	writeScannerProcess(
		t,
		root,
		200,
		"worker",
		"20",
		[]string{"", "first", "", "second", "ignored"},
	)

	var scanner Scanner
	defer scanner.Close()

	allArgs := func(process Process) bool {
		return process.Executable == "/usr/bin/interpreter"
	}

	processes, err := scanner.scan(
		t.Context(),
		root,
		"boot",
		nil,
		func(Process) bool { return false },
		allArgs,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(processes) != 2 ||
		!slices.Equal(processes[0].Args, []string{"interpreter", long, "agent"}) ||
		!slices.Equal(processes[1].Args, []string{"first", "second"}) {
		t.Fatalf("argument inventories = %#v", processes)
	}
}

func writeScannerProcess(
	t *testing.T,
	root string,
	pid int,
	name string,
	start string,
	args []string,
) string {
	t.Helper()

	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o700); err != nil {
		t.Fatal(err)
	}

	fields := []string{"S", "1", "42"}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}

	fields = append(fields, start)
	for file, data := range map[string]string{
		"stat":    strconv.Itoa(pid) + " (" + name + ") " + strings.Join(fields, " ") + "\n",
		"cmdline": strings.Join(args, "\x00") + "\x00",
		"environ": "PANE=7\x00",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for file, target := range map[string]string{
		"exe":  "/usr/bin/" + name,
		"cwd":  "/project/first",
		"fd/0": "/dev/pts/7",
	} {
		if err := os.Symlink(target, filepath.Join(dir, file)); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func replaceScannerLink(t *testing.T, path string, target string) {
	t.Helper()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}
