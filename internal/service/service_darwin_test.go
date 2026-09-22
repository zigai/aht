//go:build darwin

package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

type statusExecutor struct {
	output []byte
	err    error
}

func (e statusExecutor) Run(context.Context, string, ...string) ([]byte, error) {
	return e.output, e.err
}

func TestLaunchdStatusClassifiesMissingService(t *testing.T) {
	t.Parallel()
	exitErr := exec.CommandContext(t.Context(), "/bin/sh", "-c", "exit 1").Run()
	if exitErr == nil {
		t.Fatal("expected exit error")
	}
	backend := darwinBackend{domain: "gui/501"}
	for _, test := range []struct {
		name    string
		output  string
		cause   error
		missing bool
	}{
		{name: "unloaded service", output: "Could not find service aht in domain for user gui: 501", cause: exitErr, missing: true},
		{name: "unavailable domain", output: "Could not find domain for user gui: 501", cause: exitErr},
		{name: "permission denied", output: "Operation not permitted", cause: os.ErrPermission},
		{name: "missing command", cause: exec.ErrNotFound},
		{name: "cancellation", output: "Could not find service", cause: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			running, _, err := backend.running(t.Context(), statusExecutor{output: []byte(test.output), err: test.cause})
			if running {
				t.Fatal("failed status command reported running")
			}
			if test.missing {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, test.cause) {
				t.Fatalf("error = %v, want %v", err, test.cause)
			}
		})
	}
}
