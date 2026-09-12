//go:build !linux && !darwin

package kimi

import (
	"errors"
	"os"
	"os/exec"
)

func duplicateStream(_ *os.File) (*os.File, func(), error) {
	return nil, nil, errors.New("owned Kimi Wire transport requires Linux or macOS")
}

func ownProcessGroup(_ *exec.Cmd) error {
	return errors.New("owned Kimi Wire transport requires Linux or macOS")
}
func killProcessGroup(_ *exec.Cmd)            {}
func nativeExitCode(exit *exec.ExitError) int { return exit.ExitCode() }
func isBrokenPipe(_ error) bool               { return false }
