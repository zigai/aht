package service

import "context"

type backend interface {
	describe() Result
	content() string
	reload(ctx context.Context, executor CommandExecutor) error
	load(ctx context.Context, executor CommandExecutor) error
	restart(ctx context.Context, executor CommandExecutor) error
	unload(ctx context.Context, executor CommandExecutor) error
	running(ctx context.Context, executor CommandExecutor) (runtimeState, error)
}

type runtimeState struct {
	Running bool
	Loaded  bool
	Message string
}
