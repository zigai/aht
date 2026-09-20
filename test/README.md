# Cross-package tests

Package tests stay beside the Go code they exercise, including public API tests
in `pkg/registry`. Group related cases into meaningful files; source and test
filenames do not need to match one to one.

This directory contains standalone suites that exercise the application across
package boundaries:

- `systemtest/`: executable workflows, installation recipes, and release artifacts.
  These tests use the `integration` build tag. Run them with `just integration`,
  or `go test -count=1 -tags=integration ./test/systemtest`.
- `hostcompat/`: installed harness compatibility against isolated local providers.
  These tests use the `compatibility` build tag. Run one installed harness with
  `just compatibility <harness>`.

`go test ./...` runs the untagged package tests. Package-local tests that require
external runtimes or system resources retain their existing integration tags.
