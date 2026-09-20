# Runtime fixtures

These scripts exercise generated harness integrations through their native registration APIs. Go integration tests embed them, copy them beside generated modules in temporary directories, and run them with Node.js, Python, or `sh`.

- `node/*.mjs`: host event scenarios and the OpenClaw SDK fixture.
- `node/*.cjs`: reporter executables used to exercise slow writes, failures, and child-process cleanup.
- `python/*.py`: Hermes host event scenarios.
- `sh/*.sh`: argument capture for generated reporters.

Keep executable scripts here instead of inside Go string literals. Pass scenario inputs explicitly through test-owned environment variables or arguments; avoid rewriting script source to select a scenario.

The Go owners are `runtime_fixture_integration_test.go` for loading, execution, and captured arguments, `generated_artifacts_integration_test.go` for generated artifacts, and `reporting_process_integration_test.go` for stalled reporters and process coordination. Native event scenarios live in `native_reporting_integration_test.go` and `generated_runtime_integration_test.go`.

Run the suite with `go test -count=1 -tags=integration ./internal/install` or the repository's `just integration` command.
