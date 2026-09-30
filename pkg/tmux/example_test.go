package tmux_test

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/registry"

	"github.com/zigai/aht/v2/pkg/tmux"
)

func ExampleLocationFromEnv() {
	env := tmux.Env{
		TMUX:     "/tmp/tmux-1000/default,1234,0",
		TMUXPane: "%0",
	}

	location := tmux.LocationFromEnv(env)
	fmt.Printf("Inside: %t, PaneID: %s\n", (location.Kind == registry.MultiplexerTmux), location.PaneID)
	// Output: Inside: true, PaneID: %0
}
