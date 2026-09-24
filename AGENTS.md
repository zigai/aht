# Repository Instructions

## Harness Adapter Requirements

Every harness integration must be implemented from the harness's current native
extension, hook, or plugin documentation. Before adding or changing a harness:

- Find and read the authoritative docs for that harness's lifecycle hooks,
  plugin system, extension API, event payloads, install paths, and update
  behavior.
- Prefer the harness's native integration surface over wrappers, shims, log
  scraping, or process scanning whenever a native surface exists.
- Use wrappers, PATH shims, tmux scans, or process inspection only as explicit
  fallback behavior, and document the limitations in code/tests/README.
- Capture the harness-native session identity, resume identity/path, cwd,
  lifecycle state, permission/input waiting state, and tmux context when the
  native API exposes them.
- Keep generated hook/plugin/extension files managed, versioned, and
  idempotently updatable so reinstalling hooks replaces stale generated code
  instead of duplicating it.
- Add tests for the installed hook/plugin shape and for the state transitions
  expected from the documented lifecycle events.
- Do not edit the "Hook Installation" section in the readme when adding new harnesses.

## Adding a Harness

Everything harness-specific lives in one adapter package. Deleting
`internal/harness/<name>/` and its catalog line must leave a building tree with
no remaining references. `internal/harness/amp` is a compact reference adapter.

1. Create `internal/harness/<name>/adapter.go` with `New()` returning a type that
   embeds `harness.BaseAdapter` built from a `harness.Definition`:
   - `ID: registry.Harness("<name>")`, `Aliases`, `ProcessNames`, and `Env` keys
     for native session id/path/project root/pid/event variables.
   - `Capabilities` reflecting what the native API actually exposes.
   - `IntegrationSource`: the reporter name generated integrations pass as
     `--reporter`. `IntegrationVersion`: the adapter's generated-artifact version.
   - `StateAuthority` (`registry.AuthorityHook` or `registry.AuthorityScreen`)
     and `ScreenFallback`. Activity authority is decided only by
     `registry.ActivityAuthority`; do not add authority logic elsewhere.
   - `ExclusiveProcess: true` unless one process hosts several native sessions.
     `CatalogCreates: true` only if native catalog listings may create sessions.
2. Implement only the optional capabilities the native surface supports, as
   methods on the adapter type:
   - `InstallPlan` (+ `InstallNextStep`/`StatusNextStep`) for managed hooks,
     plugins, or extensions; templates live in `assets/`.
   - `ResumeCommand`, `PayloadCompatible`/`PayloadDefaults`, `HandleHook` for
     request/response hooks, `ObservableProcess`, and `RunWire` where applicable.
   - `LifecycleDefaults` when native event names or resume sources differ from
     the generic mapping in `internal/harness/lifecycle.go`
     (`harness.TranslateLifecycle`).
   - `transcript.go`: `Transcript() transcript.Reader` for history search. Reuse
     the shared grammars in `internal/harness/transcript` before writing a parser.
   - `title.go`: `SessionTitles`. Reuse the transcript parser when titles come
     from the same native files.
   - `manifest.go` + `assets/screen.toml`: `ScreenManifest` for screen authority
     or screen fallback.
   - `distribution.go`: `Distribution()` with `Directory` equal to the adapter
     folder name. The architecture test derives name ownership from it.
3. Generated integrations report with
   `aht report <name> --reporter <IntegrationSource> --reporter-version <version>`,
   plus `--sequence` for ordered reporters and `--multi-session` when one process
   reports several native identities. Never encode aht metadata as
   `--attribute aht_*`; native metadata may use ordinary `--attribute key=value`.
   Bump the adapter's integration version (or `harness.IntegrationVersion` for
   shared command hooks) whenever generated content changes, so
   `aht manage integrations status` reports stale artifacts and
   `aht manage integrations upgrade` replaces them.
4. Register the harness:
   - Add `<name>.New()` to `adapters` in `internal/harness/catalog/catalog.go`.
   - Add the public `Harness<Name>` constant to `pkg/aht/harnesses.go`, the only
     place public harness names are declared.
   - Add `<Name>` to the harness-name `forbidigo` pattern in `.golangci.yaml`.
   - Add the harness to the README's supported-harness list.
   - Add a host contract with the native docs URLs to
     `test/hostcompat/contracts_test.go`.
5. Do not reference harness names or add per-harness switches in
   `pkg/registry`, `pkg/history`, `internal/cli`, `internal/observer`, or
   `internal/agentstate`. Dispatch through `internal/harness/catalog`.
   `depguard`, `forbidigo`, and `internal/architecture/conventions_test.go`
   enforce this.
6. Tests:
   - `adapter_test.go` for the definition, payload defaults, and lifecycle
     translation.
   - Installed-shape tests in `internal/install` (for example
     `typescript_install_test.go`), and generated runtime fixtures in
     `internal/install/testdata/node` used by
     `generated_runtime_integration_test.go`.
   - Resume commands in `internal/harness/catalog/catalog_test.go`, history
     fixtures in `pkg/history/history_test.go`, and title capability in
     `pkg/harness/capabilities_test.go` when those capabilities exist.
7. Verify with `just lint`, `go test ./...`, `just integration`, and
   `just compatibility-tests`.
