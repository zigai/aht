//go:build linux

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	linuxUnitName = "aht-observer.service"
	platformName  = "linux"
	managerName   = "systemd"
)

var _ backend = (*linuxBackend)(nil)

type linuxBackend struct {
	path     string
	rendered string
}

// RenderSystemdUnit returns the exact managed user unit for options.
func RenderSystemdUnit(opts Options) (string, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return "", err
	}

	return strings.Join([]string{
		"# " + ManagedMarker,
		"# version: " + strconv.Itoa(ManagedVersion),
		"[Unit]",
		"Description=AHT observer",
		// Keep trying after recoverable persistence failures such as a full disk.
		// RestartSec bounds the retry rate without a permanent start-limit stop.
		"StartLimitIntervalSec=0",
		"",
		"[Service]",
		"ExecStart=" + systemdCommand(trackerCommand(normalized)),
		"Restart=on-failure",
		"RestartSec=30s",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n"), nil
}

func platformBackend(opts Options) (backend, error) {
	rendered, err := RenderSystemdUnit(opts)
	if err != nil {
		return nil, err
	}

	return definitionBackend(rendered)
}

func definitionBackend(rendered string) (backend, error) {
	path, err := definitionPath()
	if err != nil {
		return nil, err
	}

	return &linuxBackend{path: path, rendered: rendered}, nil
}

func definitionPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}

	return filepath.Join(base, "systemd", "user", linuxUnitName), nil
}

func (b *linuxBackend) describe() Result {
	return describeDefinition(b.path)
}
func (b *linuxBackend) content() string { return b.rendered }

func (b *linuxBackend) reload(ctx context.Context, executor CommandExecutor) error {
	if output, err := executor.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return wrapManagerError("systemctl daemon-reload", output, err)
	}

	return nil
}

func (b *linuxBackend) load(ctx context.Context, executor CommandExecutor) error {
	if output, err := executor.Run(
		ctx,
		"systemctl",
		"--user",
		"enable",
		"--now",
		linuxUnitName,
	); err != nil {
		return wrapManagerError("systemctl enable observer", output, err)
	}

	return nil
}

func (b *linuxBackend) restart(ctx context.Context, executor CommandExecutor) error {
	if err := b.reload(ctx, executor); err != nil {
		return err
	}

	if output, err := executor.Run(
		ctx,
		"systemctl",
		"--user",
		"restart",
		linuxUnitName,
	); err != nil {
		return wrapManagerError("systemctl restart observer", output, err)
	}

	return nil
}

func (b *linuxBackend) unload(ctx context.Context, executor CommandExecutor) error {
	output, err := executor.Run(ctx, "systemctl", "--user", "disable", "--now", linuxUnitName)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("unload systemd service: %w", err)
	}

	if err != nil && !managerMissing(output) {
		return wrapManagerError("systemctl disable observer", output, err)
	}

	return nil
}

func (b *linuxBackend) running(
	ctx context.Context,
	executor CommandExecutor,
) (runtimeState, error) {
	output, err := executor.Run(ctx, "systemctl", "--user", "is-active", "--quiet", linuxUnitName)
	if err == nil {
		return runtimeState{Running: true, Loaded: true, Message: "running"}, nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return runtimeState{}, fmt.Errorf("checking systemd service status: %w", err)
	}
	// systemctl uses LSB status codes: 3 is inactive and 4 is an unknown unit.
	const (
		inactive = 3
		unknown  = 4
	)
	if exitErr, ok := errors.AsType[*exec.ExitError](
		err,
	); ok &&
		(exitErr.ExitCode() == inactive || exitErr.ExitCode() == unknown) {
		return runtimeState{
			Running: false,
			Loaded:  false,
			Message: "not running",
		}, nil
	}

	return runtimeState{}, wrapManagerError("checking systemd service status", output, err)
}

func systemdCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, systemdArg(arg))
	}

	return strings.Join(quoted, " ")
}

func systemdArg(value string) string {
	if value == "" {
		return "\"\""
	}

	value = strings.ReplaceAll(strings.ReplaceAll(value, "%", "%%"), "$", "$$")
	if strings.IndexFunc(
		value,
		func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' || r == '\\' },
	) < 0 {
		return value
	}

	return strconv.Quote(value)
}
