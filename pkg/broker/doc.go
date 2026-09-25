// Package broker provides a lightweight client and wire protocol
// for communicating with the local AHT realtime broker over a Unix domain socket.
//
// Use [NewClientForSocket] or [NewClient] to connect directly to the broker daemon.
// Unlike [github.com/zigai/aht/v2/pkg/client], operations against a broker Client
// never fall back to durable disk files or take filesystem locks; if the broker
// is stopped, operations fail immediately with [ErrUnavailable].
// Context cancellation interrupts both request and subscription-handshake I/O.
// Responses are newline-delimited JSON frames capped at [MaxResponseBytes];
// oversized frames fail with [ErrProtocol]. Close subscriptions to cancel and
// join their reader goroutines.
package broker
