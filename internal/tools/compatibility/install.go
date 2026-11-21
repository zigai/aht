package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
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
	version := a.getenv("AHT_COMPAT_VERSION")

	dependencyDate, err := a.dependencyDate(ctx, spec, version)
	if err != nil {
		return err
	}

	commands, err := installationCommands(spec, version, dependencyDate, a.workDirectory(), bin)
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
		return appendText(path, bin+"\n")
	}

	return nil
}

func (a application) dependencyDate(
	ctx context.Context,
	spec harnessSpec,
	version string,
) (time.Time, error) {
	if spec.Source != harness.DistributionNPM || spec.Install != nil {
		return time.Time{}, nil
	}

	if _, err := parseVersion(version); err != nil {
		return time.Time{}, err
	}

	return a.releaseClient().npmDependencyDate(ctx, spec, version)
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

func installationCommands(
	spec harnessSpec,
	version string,
	dependencyDate time.Time,
	work string,
	bin string,
) ([]installCommand, error) {
	if spec.Source != harness.DistributionWeekly {
		if _, err := parseVersion(version); err != nil {
			return nil, err
		}
	} else if version != "" {
		return nil, fmt.Errorf(
			"%w: weekly harnesses do not support pinned installs",
			errCompatibility,
		)
	}

	if spec.Install != nil {
		return spec.Install(version, work, bin), nil
	}

	switch spec.Source {
	case harness.DistributionNPM:
		if dependencyDate.IsZero() {
			return nil, fmt.Errorf(
				"%w: npm install of %s needs a dependency date",
				errCompatibility,
				spec.ID,
			)
		}

		return []installCommand{
			{
				Name: "npm",
				Args: []string{
					"install",
					"--global",
					"--before=" + dependencyDate.UTC().Format(time.RFC3339Nano),
					spec.Package + "@" + version,
				},
				Env: nil,
			},
		}, nil
	case harness.DistributionPyPI:
		args := []string{"tool", "install"}
		if spec.Python != "" {
			args = append(args, "--python", spec.Python)
		}

		args = append(args, spec.Package+spec.PackageExtras+"=="+version)

		return []installCommand{{Name: "uv", Args: args, Env: nil}}, nil
	case harness.DistributionGitHub, harness.DistributionChannel, harness.DistributionWeekly:
		// These sources install only through Distribution.Install.
	}

	return nil, fmt.Errorf("%w: no installer for %s", errCompatibility, spec.ID)
}
