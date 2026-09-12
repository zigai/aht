package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	commandTimeout   = 10 * time.Minute
	commandWaitDelay = 5 * time.Second
)

type installCommand struct {
	Name string
	Args []string
	Env  []string
}

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
	// Versions are arguments, never interpolated into shell programs.
	switch spec.Source {
	case "npm":
		return []installCommand{{Name: "npm", Args: []string{"install", "--global", spec.Package + "@" + version}, Env: nil}}, nil
	case "pypi":
		packageName := spec.Package
		if spec.ID == "hermes" {
			packageName += "[acp]"
		}
		return []installCommand{{Name: "uv", Args: []string{"tool", "install", packageName + "==" + version}, Env: nil}}, nil
	case "github":
		return githubInstallation(spec, version, work, bin)
	case "channel":
		script := filepath.Join(work, "install-grok.sh")
		return []installCommand{downloadCommand("https://x.ai/cli/install.sh", script), {Name: "bash", Args: []string{script, version}, Env: []string{"GROK_BIN_DIR=" + bin}}}, nil
	case "weekly":
		script := filepath.Join(work, "install-cursor.sh")
		return []installCommand{downloadCommand("https://cursor.com/install", script), {Name: "bash", Args: []string{script}, Env: nil}}, nil
	default:
		return nil, fmt.Errorf("%w: no installer for %s", errCompatibility, spec.ID)
	}
}

func githubInstallation(spec harnessSpec, version, work, bin string) ([]installCommand, error) {
	asset := filepath.Join(work, spec.Asset)
	commands := []installCommand{downloadCommand("https://github.com/"+spec.Repo+"/releases/download/"+version+"/"+spec.Asset, asset)}
	switch spec.ID {
	case "goose":
		commands = append(commands, installCommand{Name: "bash", Args: []string{asset}, Env: []string{"GOOSE_VERSION=" + version, "GOOSE_BIN_DIR=" + bin, "CONFIGURE=false"}})
	case "agy":
		// The official release archive contains a binary named "antigravity".
		commands = append(commands,
			installCommand{Name: "tar", Args: []string{"-xzf", asset, "-C", work, "antigravity"}, Env: nil},
			installCommand{Name: "install", Args: []string{"-m", "0755", filepath.Join(work, "antigravity"), filepath.Join(bin, "agy")}, Env: nil})
	default:
		return nil, fmt.Errorf("%w: no GitHub installer for %s", errCompatibility, spec.ID)
	}
	return commands, nil
}

func downloadCommand(url, path string) installCommand {
	return installCommand{Name: "curl", Args: []string{"--fail", "--silent", "--show-error", "--location", "--retry", "3", "--max-time", "180", "--output", path, url}, Env: nil}
}
