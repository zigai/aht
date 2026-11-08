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
- Reproduce harness behavior only in a scratch home: HOME plus the adapter's config `Env` keys pointed under `~/.cache`. When the harness must take a model turn, use `fakeprovider start <harness>`, which creates that root. Never run a harness, its hooks, or `aht manage integrations` against the real home.
- Do not edit the "Hook Installation" section in the readme when adding new harnesses.

## Adding a Harness

Harness integrations live in one adapter package; fakeprovider launch plans live
in `pkg/fakeprovider/launch/<name>.go`. Deleting both and their catalog/table entries
must leave a building tree. `internal/harness/amp` is a compact reference adapter.

1. Create `internal/harness/<name>/adapter.go` with `New()` returning a type that
   embeds `harness.BaseAdapter` built from a `harness.Definition`:
   - `ID: registry.Harness("<name>")`, `Aliases`, `ProcessNames`, and `Env` keys
     for native session id/path/project root/pid/event variables.
   - `Capabilities` reflecting what the native API actually exposes.
   - `Distribution` with `Directory` equal to the adapter folder name, and
     `DetailCapabilities` for native and screen detail support.
   - `IntegrationSource`: the reporter name generated integrations pass as
     `--reporter`. `IntegrationVersion`: the adapter's generated-artifact version.
   - `StateAuthority` (`registry.AuthorityHook` or `registry.AuthorityScreen`)
     and `ScreenFallback`. Activity authority is decided only by
     `registry.ActivityAuthority`; do not add authority logic elsewhere.
     Set `ScreenManifest` for screen authority or screen fallback.
   - `ExclusiveProcess: true` unless one process hosts several native sessions.
     `CatalogCreates: true` only if native catalog listings may create sessions.
2. Implement the required methods on the adapter type:
   - `InstallPlan` (+ `InstallNextStep`/`StatusNextStep`) for managed hooks,
     plugins, or extensions; templates live in `assets/`. Build command hooks
     with `harness.CommandHookSpecs` and render templates with
     `harness.RenderScriptTemplate`.
   - `ResumeCommand` for native resume identities.
   - `LifecycleEventAttribute` and `LifecycleSourceAttributes` in the
     `Definition` when native event names or resume sources live in attributes
     the generic mapping in `internal/harness/lifecycle.go` does not read.
   - `transcript.go`: `Transcript() transcript.Reader` for history search. Reuse
     the shared grammars in `internal/harness/transcript` before writing a parser.
   - `distribution.go` for custom distribution install commands when needed.
     The architecture test derives name ownership from `Definition.Distribution`.
   - `locations.go`: `Locations(projectDir)` with the config, instruction,
     skill, command, and MCP paths the native docs name. Every adapter needs it.
   - Optional, where supported: `PayloadCompatible`/`PayloadDefaults`,
     `HandleHook`, `ObservableProcess`, and `SessionTitles`. Reuse the
     transcript parser when titles come from the same native files.
   - When the harness supports a custom model endpoint, add a launch plan in
     `pkg/fakeprovider/launch/<name>.go` and register it in that package's
     table. The `fakeprovider` command and `test/hostcompat` launch harnesses
     only through this package.
3. Generated integrations report with
   `aht report <name> --reporter <IntegrationSource> --reporter-version <version>`,
   plus `--sequence` for ordered reporters and `--multi-session` when one process
   reports several native identities. Never encode aht metadata as
   `--attribute aht_*`; native metadata may use ordinary `--attribute key=value`.
   Bump the adapter's integration version whenever its generated content
   changes, including changes to shared templates or hook commands, so
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
   - `adapter_test.go` for native payloads, lifecycle translation, and
     adapter-specific behavior.
   - Installed-shape tests in `internal/install` (for example
     `typescript_install_test.go`), and generated runtime fixtures in
     `internal/install/testdata/node` used by
     `generated_runtime_integration_test.go`.
   - Catalog properties cover aliases, native resume identities, and title
     capabilities automatically. Keep adapter payload fixtures in
     `adapter_test.go` and history fixtures in `pkg/history/history_test.go`.
   - The adapter is picked up automatically by the generated lifecycle
     properties in `internal/harness/catalog/lifecycle_property_test.go`. It
     must pass them without per-harness exceptions.
7. Verify with `just check`.

## Conventions

- Predicate methods on adjectives drop `Is`: `Valid()`, not `IsValid()`.
- Options variants keep the `*WithOptions` or `*WithEnv` suffix, each beside a plain base such as `Summary`.
- `Find` returning `(T, bool)` is fine for keyed lookups.
- Name options parameters `opts`.
- Tests of test helpers and fixtures are fine here.

## Harness behavior

- aht supports OpenCode 2 only, from `@opencode/cli`. Kilo uses a different API and events, so keep its paths separate.
- A Claude `Stop` with background shell tasks counts as running, even for never-ending watchers.
- Pi reports idle only on `agent_settled`, not `agent_end`.

## Invariants and Simulation

Registry, journal, and observer behavior is checked by named invariants and
seeded simulations:

- Session invariants live in `internal/registrycheck` (`core` holds the rules
  `pkg/registry` tests share); cross-step registry invariants live in
  `pkg/registry/invariants_test.go`. When a change introduces a new rule about
  session state, add it there and record the situation as a required
  `registrycheck.Coverage` name so the property cannot pass vacuously.
- Registry file I/O goes through `registryFSFor` (`pkg/registry/fs.go`), so the
  journal crash tests see every write, sync, and rename.
- A failing randomized test prints its seed. Reproduce from that seed, fix the
  code, and keep the seed as a regression case.
- Run `just explore` (extended seeds and bounded fuzzing) before merging
  registry, journal, or observer changes.
