package processinfo

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
)

func TestParseDarwinProcArgsPreservesArgumentBoundaries(t *testing.T) {
	t.Parallel()

	want := []string{"tmux", "-S", "/tmp/agent sessions.sock", "server"}
	data := make([]byte, 0, 96)
	data = binary.LittleEndian.AppendUint32(data, 4)

	data = append(data, "/usr/local/bin/tmux\x00\x00"...)
	for _, arg := range want {
		data = append(data, arg...)
		data = append(data, 0)
	}

	data = append(
		data,
		"PATH=/usr/bin\x00AHT_PROCESS_ROLE=history-refresh\x00AHT_PROCESS_ROLE=other\x00"...,
	)

	executable, got, environment, err := parseDarwinProcArgs(data)
	if err != nil {
		t.Fatal(err)
	}

	if got := parseEnvironment(
		environment,
		[]string{ChildRoleEnv},
	); len(got) != 1 ||
		got[ChildRoleEnv] != "history-refresh" {
		t.Fatalf("selected environment = %#v", got)
	}

	if executable != "/usr/local/bin/tmux" || !slices.Equal(got, want) {
		t.Fatalf("parsed executable %q arguments %#v, want %#v", executable, got, want)
	}
}

func TestParseDarwinProcArgsRejectsTruncation(t *testing.T) {
	t.Parallel()

	data := make([]byte, 0, 24)
	data = binary.LittleEndian.AppendUint32(data, 2)

	data = append(data, "/usr/bin/tmux\x00\x00tmux\x00"...)
	if _, _, _, err := parseDarwinProcArgs(data); !errors.Is(err, errInvalidDarwinProcArgs) {
		t.Fatalf("truncated arguments error = %v", err)
	}
}
