//go:build !linux && !darwin

package tmux

import "context"

// ServerProcessFrom reports whether a current-user process, given its kernel
// command name and arguments, is a tmux server that discovery should query.
func ServerProcessFrom(int, string, []string) (ServerProcess, bool) {
	return ServerProcess{PID: 0, Args: nil}, false
}

func listCurrentUserTmuxServers(ctx context.Context) ([]ServerProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}
