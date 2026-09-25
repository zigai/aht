package aht_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/zigai/aht/v2/pkg/aht"
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

func ExampleLiveness() {
	sessions := []aht.Session{
		{ID: "codex-1", Liveness: aht.Live{Activity: aht.ActivityRunning}},
		{ID: "pi-2", Liveness: aht.Gone{Reason: "process_gone"}},
	}
	for _, session := range sessions {
		switch liveness := session.Liveness.(type) {
		case aht.Live:
			fmt.Println(session.ID, "live", liveness.Activity)
		case aht.Gone:
			fmt.Println(session.ID, "gone", liveness.Reason)
		case aht.Unknown:
			fmt.Println(session.ID, "unknown", liveness.Activity)
		}
	}
	// Output:
	// codex-1 live running
	// pi-2 gone process_gone
}

//nolint:testableexamples // requires a running tracker, so this example is compiled but not run.
func ExampleClient_Watch() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client := aht.New(aht.Config{})
	err := client.Watch(ctx, aht.Filter{Presence: aht.PresenceLive}, func(state aht.StateSnapshot) error {
		fmt.Printf("revision %d: %d live sessions\n", state.Revision, len(state.Sessions))
		return nil // a non-nil error stops watching and is returned
	})
	if err != nil {
		log.Print(err)
	}
}

//nolint:testableexamples // titles come from machine-specific harness files, so this example is compiled but not run.
func ExampleLookupTitles() {
	ctx := context.Background()
	sessions, err := aht.New(aht.Config{}).List(ctx, aht.Filter{Presence: aht.PresenceLive})
	if err != nil {
		log.Fatal(err)
	}
	titles, err := aht.LookupTitles(ctx, sessions)
	if err != nil {
		log.Print(err) // titles still holds every title that was read
	}
	for i, session := range sessions {
		fmt.Println(session.ID, titles[i])
	}
}

//nolint:testableexamples // searches machine-specific archives, so this example is compiled but not run.
func ExampleHistoryCatalog_Search() {
	catalog := aht.HistoryCatalog{
		Sources: []aht.HistorySource{
			{Harness: aht.HarnessPi, Path: "/archive/pi/sessions"},
			{Harness: aht.HarnessClaude, Path: "/archive/claude/projects"},
		},
		IndexPath: "/archive/aht-history.sqlite",
	}
	result, err := catalog.Search(context.Background(), aht.HistoryQuery{Text: "migration", Dir: "/work/app"})
	if err != nil && !errors.Is(err, aht.ErrHistoryIncomplete) {
		log.Fatal(err)
	}
	for _, match := range result.Matches {
		fmt.Println(match.Conversation.Harness, match.Conversation.Title)
	}
}

//nolint:testableexamples // inspects and modifies local harness configuration, so this example is compiled but not run.
func ExampleNewManager() {
	ctx := context.Background()
	manager := aht.NewManager(aht.ManagerConfig{})

	status, err := manager.IntegrationStatus(ctx, aht.HarnessCodex)
	if err != nil {
		log.Fatal(err)
	}
	if status.Status == aht.ArtifactStale {
		if _, err := manager.InstallIntegration(ctx, aht.HarnessCodex, aht.IntegrationOptions{}); err != nil {
			log.Fatal(err)
		}
	}
}

//nolint:testableexamples // writes to the local registry, so this example is compiled but not run.
func ExampleClient_Observe() {
	idle := aht.ActivityIdle
	_, err := aht.New(aht.Config{}).Observe(context.Background(), aht.Observation{
		Harness: aht.HarnessPi,
		At:      time.Now(),
		Subject: aht.ObservationIdentity{SessionID: "native-session-id"},
		Evidence: &aht.Report{
			Reporter: aht.Reporter{Integration: "my-tool", Version: 1},
			Event:    "turn_end",
			Activity: &idle,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
