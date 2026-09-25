package client_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

func ExampleClient_List() {
	directory, err := os.MkdirTemp("", "aht-client-example")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()

	storePath := filepath.Join(directory, "sessions.json")
	presence := registry.PresenceLive
	activity := registry.ActivityRunning
	if _, err := registry.NewJournal(storePath, catalog.Rules{}).Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "example"}, Evidence: &registry.Report{Claim: &presence, Activity: &activity}}); err != nil {
		panic(err)
	}

	aht := client.New(client.Config{
		StorePath:  storePath,
		SocketPath: filepath.Join(directory, "offline.sock"),
	})
	sessions, err := aht.List(
		context.Background(),
		client.Filter{Presence: client.PresenceLive},
	)
	if err != nil {
		panic(err)
	}

	fmt.Printf("%s: %s\n", sessions[0].Harness, *sessions[0].Activity())
	// Output: pi: running
}

func ExampleClient_Wait() {
	directory, err := os.MkdirTemp("", "aht-client-wait-example")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()

	storePath := filepath.Join(directory, "sessions.json")
	presence := registry.PresenceLive
	activity := registry.ActivityIdle
	observed, err := registry.NewJournal(storePath, catalog.Rules{}).Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "wait-example"}, Evidence: &registry.Report{Claim: &presence, Activity: &activity}})
	if err != nil {
		panic(err)
	}

	aht := client.New(client.Config{
		StorePath: storePath,
		Mode:      client.ModeDurableOnly,
	})
	res, err := aht.Wait(context.Background(), client.WaitOptions{
		ID:       observed.ID,
		Activity: client.ActivityIdle,
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("%s: %s\n", res.Session.Harness, *res.Session.Activity())
	// Output: pi: idle
}
