package aht_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"

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

//nolint:testableexamples // local history is machine-specific, so this example is compiled but not run.
func ExampleSearchHistory() {
	result, err := aht.SearchHistory(context.Background(), aht.HistoryQuery{
		Text:         "refresh token",
		IncludeTools: true,
		Limit:        20,
	})
	if err != nil && !errors.Is(err, aht.ErrHistoryIncomplete) {
		log.Fatal(err)
	}
	if errors.Is(err, aht.ErrHistoryIncomplete) {
		// Some sources could not be searched; result still holds partial matches.
		fmt.Printf("partial results: %d matches, %d issues\n", len(result.Matches), len(result.Issues))
	}
	for _, match := range result.Matches {
		conversation := match.Conversation
		fmt.Printf("%s %s %s\n", conversation.Harness, conversation.SessionID, conversation.UpdatedAt.Format(time.RFC3339))
	}
}
