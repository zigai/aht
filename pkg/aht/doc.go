// Package aht provides the primary high-level API for inspecting, filtering,
// and streaming agent-harness sessions tracked by AHT.
//
// It exports the central [Client], operational modes, and re-exports core domain
// models ([Session], [Filter], [Presence], [Activity], [Harness]) so that consumers
// can interact with AHT through a single import. [LookupTitles] reads optional
// harness display metadata on demand, independently of the live registry.
package aht
