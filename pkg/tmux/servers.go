package tmux

import (
	"context"
	"path/filepath"
	"strings"
)

type serverSpec struct {
	Identity string
	Args     []string
}

func discoverServers(ctx context.Context, env Env, lister ServerProcessLister) ([]serverSpec, error) {
	processes, err := lister(ctx)
	if err != nil {
		return nil, err
	}

	servers := make([]serverSpec, 0, len(processes)+1)
	seen := make(map[string]struct{}, len(processes)+1)
	add := func(server serverSpec) {
		key := server.Identity
		if key == "" {
			key = "default"
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		servers = append(servers, server)
	}

	if socket := tmuxServerSocket(env.TMUX); socket != "" {
		add(serverSpec{Identity: socket, Args: []string{"-S", socket}})
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
		return serverSpec{Identity: "", Args: nil}, false
	}
	base := filepath.Base(args[0])
	if !isTmuxBinaryName(base) {
		return serverSpec{Identity: "", Args: nil}, false
	}
	for index, arg := range args {
		switch arg {
		case "-S":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return serverSpec{Identity: "", Args: nil}, false
			}
			socket := args[index+1]
			return serverSpec{Identity: socket, Args: []string{"-S", socket}}, true
		case "-L":
			if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
				return serverSpec{Identity: "", Args: nil}, false
			}
			name := args[index+1]
			return serverSpec{Identity: "-L:" + name, Args: []string{"-L", name}}, true
		}
	}
	return serverSpec{Identity: "default", Args: nil}, true
}

func isTmuxBinaryName(name string) bool {
	base := filepath.Base(name)
	if base == "tmux" || base == "tmux:" || strings.HasPrefix(base, "tmux: server") {
		return true
	}
	clean := strings.TrimSuffix(strings.TrimSuffix(base, ".bin"), ".real")
	return clean == "tmux" || clean == "tmux:"
}
