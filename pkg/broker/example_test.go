package broker_test

import (
	"context"
	"fmt"

	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

func ExampleNewClientForSocket() {
	client := broker.NewClientForSocket("/path/to/aht.sock")
	fmt.Printf("Socket: %s\n", client.SocketPath())
	// Output: Socket: /path/to/aht.sock
}

func ExampleClient_List() {
	client := broker.NewClientForSocket("/path/to/offline.sock")
	_, err := client.List(context.Background(), registry.Filter{})
	if broker.IsUnavailable(err) {
		fmt.Println("broker is offline")
	}
	// Output: broker is offline
}
