//go:build linux

package processinfo

import (
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
	if got.PID != 123 || got.PPID != 41 || got.ProcessGroupID != 42 || !got.Foreground || got.StartIdentity != "987654" {
		t.Fatalf("parsed process = %#v", got)
	}
}

func TestLinuxEnvironmentHintsKeepFirstRecognizedValues(t *testing.T) {
	t.Parallel()
	got := parseLinuxEnvironmentHints([]byte("PATH=/usr/bin\x00NOT_AHT_AGENT=wrong\x00AHT_AGENT= codex \x00AHT_AGENT=pi\x00HERDR_PANE_ID= \x00HERDR_PANE_ID=late\x00HERDR_SOCKET_PATH=/tmp/socket=name\x00HERDR_SESSION= work \x00ZELLIJ_PANE_ID=7\x00ZELLIJ_SESSION_NAME= tabs\x00OTHER=value\x00"))
	want := linuxEnvironmentHints{agent: "codex", herdrServer: "/tmp/socket=name", herdrSession: "work", zellijPane: "7", zellijSession: "tabs"}
	if got != want {
		t.Fatalf("environment hints = %#v, want %#v", got, want)
	}
}

func TestReadLinuxProcessCapturesMultiplexerIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, environment, kind, server, session, pane string
	}{
		{name: "zellij", environment: "ZELLIJ_SESSION_NAME=work\x00ZELLIJ_PANE_ID=7\x00", kind: "zellij", session: "work", pane: "7"},
		{name: "herdr precedence", environment: "ZELLIJ_SESSION_NAME=work\x00ZELLIJ_PANE_ID=7\x00HERDR_PANE_ID=9\x00HERDR_SESSION=main\x00HERDR_SOCKET_PATH=/tmp/herdr.sock\x00", kind: "herdr", server: "/tmp/herdr.sock", session: "main", pane: "9"},
		{name: "empty first herdr pane", environment: "HERDR_PANE_ID= \x00HERDR_PANE_ID=9\x00ZELLIJ_SESSION_NAME=work\x00ZELLIJ_PANE_ID=7\x00", kind: "zellij", session: "work", pane: "7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("123 (codex) S 1 42 42 0 0 0 0 0 0 0 0 0 0 0 0 0 0 987654 0 0"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(test.environment), 0o600); err != nil {
				t.Fatal(err)
			}
			process, err := readLinuxProcess(dir, 123, "boot")
			if err != nil {
				t.Fatal(err)
			}
			if process.MultiplexerKind != test.kind || process.MultiplexerServer != test.server || process.MultiplexerSession != test.session || process.MultiplexerPane != test.pane {
				t.Fatalf("multiplexer identity = %#v", process)
			}
		})
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
