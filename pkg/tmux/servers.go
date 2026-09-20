package tmux

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	gotmux "github.com/zigai/gotmux/tmux"
)

type serverSpec struct {
	Identity string
}

func discoverServers(ctx context.Context, options ListOptions) ([]serverSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover tmux servers: %w", err)
	}
	sockets := options.SocketPaths
	if sockets == nil {
		var err error
		sockets, err = gotmux.DiscoverSockets()
		if err != nil {
			return nil, fmt.Errorf("discover tmux sockets: %w", err)
		}
	}
	// gotmux discovers standard socket directories. Process inspection remains
	// a fallback for custom -S paths outside those directories; it can only
	// recover paths that the OS exposes in the server's process arguments.
	processes, err := options.ServerProcesses(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover tmux servers: %w", err)
	}

	servers := make([]serverSpec, 0, len(processes)+len(sockets)+1)
	seen := make(map[string]struct{}, len(processes)+len(sockets)+1)
	add := func(server serverSpec) {
		if _, exists := seen[server.Identity]; exists {
			return
		}
		seen[server.Identity] = struct{}{}
		servers = append(servers, server)
	}

	if socket := tmuxServerSocket(options.Env.TMUX); socket != "" {
		add(serverSpec{Identity: socket})
	}
	for _, socket := range sockets {
		if socket != "" {
			add(serverSpec{Identity: socket})
		}
	}

	for _, process := range processes {
		server, ok := serverSpecFromArgs(process.Args)
		if !ok {
			continue
		}
		add(server)
	}
	return servers, nil
}

func serverSpecFromArgs(args []string) (serverSpec, bool) {
	if len(args) == 0 {
		return serverSpec{Identity: ""}, false
	}
	base := filepath.Base(args[0])
	if !isTmuxBinaryName(base) {
		return serverSpec{Identity: ""}, false
	}
	// Some tmux builds expose a bare -d daemon process marker rather than a
	// native root flag. It carries no endpoint, so retain the default fallback.
	if len(args) == 2 && args[1] == "-d" {
		return serverSpec{Identity: "default"}, true
	}
	parsed, err := gotmux.ParseCommandLine(args[1:])
	if err != nil {
		return serverSpec{Identity: ""}, false
	}
	if socket := parsed.Config.SocketPath; socket != "" {
		return serverSpec{Identity: socket}, true
	}
	if name := parsed.Config.SocketName; name != "" {
		return serverSpec{Identity: "-L:" + name}, true
	}
	return serverSpec{Identity: "default"}, true
}

func isTmuxBinaryName(name string) bool {
	base := filepath.Base(name)
	if base == "tmux" || base == "tmux:" || strings.HasPrefix(base, "tmux: server") {
		return true
	}
	clean := strings.TrimSuffix(strings.TrimSuffix(base, ".bin"), ".real")
	return clean == "tmux" || clean == "tmux:"
}
