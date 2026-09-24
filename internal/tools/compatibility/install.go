package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/zigai/aht/internal/harness"
)

const (
	commandTimeout   = 10 * time.Minute
	commandWaitDelay = 5 * time.Second
)

type installCommand = harness.DistributionCommand

func (a application) install(ctx context.Context) error {
	spec, err := findHarness(a.getenv("AHT_COMPAT_HARNESS"))
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	bin := filepath.Join(home, ".local", "bin")
	commands, err := installationCommands(spec, a.getenv("AHT_COMPAT_VERSION"), a.workDirectory(), bin)
	if err != nil {
		return err
	}
	for _, directory := range []string{a.workDirectory(), bin} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create install directory: %w", err)
		}
	}
	for _, command := range commands {
		if err := a.execute(ctx, command); err != nil {
			return err
		}
	}
	if path := a.getenv("GITHUB_PATH"); path != "" {
		return appendText(path, bin+"\n"+filepath.Join(home, ".cursor", "bin")+"\n")
	}
	return nil
}

func (a application) execute(ctx context.Context, command installCommand) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Env = append(cmd.Environ(), command.Env...)
	cmd.Stdout = a.stdout
	cmd.Stderr = a.stderr
	cmd.WaitDelay = commandWaitDelay
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", command.Name, err)
	}
	return nil
}

func installationCommands(spec harnessSpec, version, work, bin string) ([]installCommand, error) {
	if spec.Source != "weekly" {
		if _, err := parseVersion(version); err != nil {
			return nil, err
		}
	} else if version != "" {
		return nil, fmt.Errorf("%w: weekly harnesses do not support pinned installs", errCompatibility)
	}
	if spec.Install != nil {
		return spec.Install(version, work, bin), nil
	}
	switch spec.Source {
	case "npm":
		return []installCommand{{Name: "npm", Args: []string{"install", "--global", spec.Package + "@" + version}, Env: nil}}, nil
	case "pypi":
		packageName := spec.Package + spec.PackageExtras
		return []installCommand{{Name: "uv", Args: []string{"tool", "install", packageName + "==" + version}, Env: nil}}, nil
	default:
		return nil, fmt.Errorf("%w: no installer for %s", errCompatibility, spec.ID)
	}
}
