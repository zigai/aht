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

func discoverServers(ctx context.Context, opts ListOptions) ([]serverSpec, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("discover tmux servers: %w", err)
	}

	sockets := opts.SocketPaths
	if sockets == nil {
		var err error

		sockets, err = gotmux.DiscoverSockets()
		if err != nil {
			return nil, fmt.Errorf("discover tmux sockets: %w", err)
		}
	}

	servers := make([]serverSpec, 0, len(sockets)+1)
	seen := make(map[string]struct{}, len(sockets)+1)
	add := func(server serverSpec) {
		// Sockets are found as paths but processes as -L names; compare both as
		// paths so one server is queried once.
		server.Identity = canonicalServerIdentity(server.Identity)
		if _, exists := seen[server.Identity]; exists {
			return
		}

		seen[server.Identity] = struct{}{}
		servers = append(servers, server)
	}

	if socket := tmuxServerSocket(opts.Env.TMUX); socket != "" {
		add(serverSpec{Identity: socket})
	}

	for _, socket := range sockets {
		if socket != "" {
			add(serverSpec{Identity: socket})
		}
	}

	// gotmux discovers standard socket directories. Process inspection remains
	// a fallback for custom -S paths outside those directories; it can only
	// recover paths that the OS exposes in the server's process arguments.
	for _, process := range opts.Processes {
		if !isServerProcess(process) {
			continue
		}

		server, ok := serverSpecFromArgs(process.Args)
		if !ok {
			continue
		}

		add(server)
	}

	return servers, nil
}

func canonicalServerIdentity(identity string) string {
	cfg, err := gotmuxConfigForIdentity(identity)
	if err != nil {
		return identity
	}

	server, err := gotmux.New(cfg)
	if err != nil {
		return identity
	}

	return server.Endpoint().String()
}

func serverSpecFromArgs(args []string) (serverSpec, bool) {
	if len(args) == 0 {
		return serverSpec{}, false
	}

	base := filepath.Base(args[0])
	if !isTmuxBinaryName(base) {
		return serverSpec{}, false
	}
	// Some tmux builds expose a bare -d daemon process marker rather than a
	// native root flag. It carries no endpoint, so retain the default fallback.
	if len(args) == 2 && args[1] == "-d" {
		return serverSpec{Identity: "default"}, true
	}

	parsed, err := gotmux.ParseCommandLine(args[1:])
	if err != nil {
		return serverSpec{}, false
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
