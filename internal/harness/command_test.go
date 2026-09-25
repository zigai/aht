package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/command"
)

func TestRunCommandOutputBudget(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			output, err := RunCommand(t.Context(), 5*time.Second, 32, binary, "-test.run=^TestCommandOutputHelper$", "--", stream)
			if !errors.Is(err, command.ErrOutputLimit) {
				t.Fatalf("error = %v, want output budget failure", err)
			}
			if output != nil {
				t.Fatalf("oversized command returned usable partial output: %q", output)
			}
		})
	}
}

func TestRunCommandCancellation(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = RunCommand(ctx, time.Second, 32, binary, "-test.run=^TestCommandOutputHelper$", "--", "stdout")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestCommandOutputHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	writer := os.Stdout
	if os.Args[len(os.Args)-1] == "stderr" {
		writer = os.Stderr
	}
	if _, err := fmt.Fprint(writer, strings.Repeat("x", 33)); err != nil {
		t.Fatal(err)
	}
}
