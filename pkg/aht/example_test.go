package aht_test

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/pkg/aht"
)

func ExampleNew() {
	client := aht.New(aht.Config{
		SocketPath: "/path/to/socket.sock",
		Mode:       aht.ModeRealtimeOnly,
	})

	fmt.Printf("Mode: %s\n", client.Mode())
	// Output: Mode: realtime
}

func ExampleClient_List() {
	client := aht.New(aht.Config{
		SocketPath: filepath.Join("/nonexistent", "offline.sock"),
		Mode:       aht.ModeRealtimeOnly,
	})

	_, err := client.List(context.Background(), aht.Filter{Presence: aht.PresenceLive})
	if aht.IsUnavailable(err) {
		fmt.Println("AHT broker is offline")
	}
	// Output: AHT broker is offline
}

func ExampleClient_Wait() {
	client := aht.New(aht.Config{
		SocketPath: filepath.Join("/nonexistent", "offline.sock"),
		Mode:       aht.ModeRealtimeOnly,
	})

	_, err := client.Wait(context.Background(), aht.WaitOptions{
		ID:       "session-id",
		Activity: aht.ActivityIdle,
	})
	if aht.IsUnavailable(err) {
		fmt.Println("AHT broker is offline")
	}
	// Output: AHT broker is offline
}
