package tmux_test

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/registry"

	"github.com/zigai/aht/v2/pkg/tmux"
)

func ExampleContextFromEnv() {
	env := tmux.Env{
		TMUX:     "/tmp/tmux-1000/default,1234,0",
		TMUXPane: "%0",
	}

	context := tmux.ContextFromEnv(env)
	fmt.Printf("Inside: %t, PaneID: %s\n", (context.Kind == registry.MultiplexerTmux), context.PaneID)
	// Output: Inside: true, PaneID: %0
}
