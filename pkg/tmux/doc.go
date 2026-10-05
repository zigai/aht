// Package tmux discovers and inspects tmux terminal multiplexer environments.
//
// It provides helpers for detecting the current tmux session, window, and pane
// from process environment variables, listing active panes with batched tmux reads,
// and sending signals to panes across running tmux servers.
package tmux
