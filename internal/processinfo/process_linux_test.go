//go:build linux

package processinfo

import (
	"maps"
	"os"
	"path/filepath"
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

	process, err := reader.read(dir, 123, "boot", []string{"ZELLIJ_PANE_ID"})
	if err != nil {
		t.Fatal(err)
	}

	if want := map[string]string{"ZELLIJ_PANE_ID": "7"}; !maps.Equal(process.Env, want) {
		t.Fatalf("environment = %#v, want %#v", process.Env, want)
	}

	process, err = reader.read(dir, 123, "boot", nil)
	if err != nil {
		t.Fatal(err)
	}

	if process.Env != nil {
		t.Fatalf("environment read without requested keys: %#v", process.Env)
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
