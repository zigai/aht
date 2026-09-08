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

const linuxUnitName = "aht-observer.service"

var _ backend = (*linuxBackend)(nil)

type linuxBackend struct {
	path     string
	rendered string
}

// RenderSystemdUnit returns the exact managed user unit for options.
func RenderSystemdUnit(options Options) (string, error) {
	normalized, err := normalizeOptions(options)
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
		"ExecStart=" + systemdArg(normalized.Binary) + " --store " + systemdArg(normalized.StorePath) + " manage tracker run --interval " + normalized.Interval.String() + " --grace-period " + normalized.GracePeriod.String() + " --quiet",
		"Restart=on-failure",
		"RestartSec=30s",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n"), nil
}

func platformBackend(options Options) (backend, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	rendered, err := RenderSystemdUnit(normalized)
	if err != nil {
		return nil, err
	}
	path, err := systemdUnitPath()
	if err != nil {
		return nil, err
	}
	return &linuxBackend{path: path, rendered: rendered}, nil
}

func systemdUnitPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "systemd", "user", linuxUnitName), nil
}

func (b *linuxBackend) describe() Result {
	return Result{Platform: "linux", Manager: "systemd", ManagedPath: b.path, ManagedVersion: ManagedVersion, Path: b.path, Version: ManagedVersion, Installed: false, Current: false, Running: false, Changed: false, Message: ""}
}
func (b *linuxBackend) content() string { return b.rendered }

func (b *linuxBackend) reload(ctx context.Context, executor CommandExecutor) error {
	if output, err := executor.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return wrapManagerError("systemctl daemon-reload", output, err)
	}
	return nil
}

func (b *linuxBackend) load(ctx context.Context, executor CommandExecutor) error {
	if output, err := executor.Run(ctx, "systemctl", "--user", "enable", "--now", linuxUnitName); err != nil {
		return wrapManagerError("systemctl enable observer", output, err)
	}
	return nil
}

func (b *linuxBackend) restart(ctx context.Context, executor CommandExecutor) error {
	if output, err := executor.Run(ctx, "systemctl", "--user", "restart", linuxUnitName); err != nil {
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

func (b *linuxBackend) running(ctx context.Context, executor CommandExecutor) (bool, string, error) {
	output, err := executor.Run(ctx, "systemctl", "--user", "is-active", "--quiet", linuxUnitName)
	if err == nil {
		return true, "running", nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, "", fmt.Errorf("checking systemd service status: %w", err)
	}
	// systemctl uses LSB status codes: 3 is inactive and 4 is an unknown unit.
	const (
		inactive = 3
		unknown  = 4
	)
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && (exitErr.ExitCode() == inactive || exitErr.ExitCode() == unknown) {
		return false, "not running", nil
	}
	return false, "", wrapManagerError("checking systemd service status", output, err)
}

func systemdArg(value string) string {
	if value == "" {
		return "\"\""
	}
	value = strings.ReplaceAll(value, "%", "%%")
	if strings.IndexFunc(value, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\\' }) < 0 {
		return value
	}
	return strconv.Quote(value)
}
