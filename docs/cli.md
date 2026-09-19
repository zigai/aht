# CLI Reference

The `aht` command-line tool tracks, inspects, and manages local coding-agent sessions.

## Global Flags

These flags apply across commands:

| Flag | Type | Description |
|---|---|---|
| `--config <path>` | `string` | Configuration file path (replaces disk tiers; `-` reads from stdin) |
| `--no-config` | `bool` | Bypass all configuration files and stdin, retaining env and defaults |
| `--store <path>` | `string` | Registry state file path (defaults to `~/.local/state/aht/state.json`) |
| `--json` | `bool` | Emit JSON output (JSON Lines for streaming commands) |
| `-V, --version` | `bool` | Print version, commit hash, and build timestamp |
| `--help` | `bool` | Help for `aht` or any subcommand (`-h` is not reserved) |

## Exit Codes

AHT emits standard semantic exit codes:

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Domain error (action failed, missing target) |
| `2` | Parse, usage, operand, or configuration error |
| `130` | Interrupted by user (`SIGINT` / Ctrl-C) |
| `141` | Broken pipe (`SIGPIPE`, silent exit) |

---

## Session Commands

### `aht list`

List known agent sessions in tabular or JSON format.

```sh
aht list [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|---|---|---|---|
| `--presence <val>` | `string` | `"all"` | Filter sessions by presence: `live`, `gone`, `unknown`, `all` |
| `--activity <val>` | `string` | `""` | Filter sessions by reported activity: `running`, `waiting`, `idle`, `unknown` |
| `--agent <name>` | `string` | `""` | Filter by harness name (un-hides harnesses in `ignore_harnesses`) |
| `--tmux-session <name>` | `string` | `""` | Filter by tmux session name |
| `--multiplexer-session <name>` | `string` | `""` | Filter by multiplexer session name |
| `--sort <field>` | `string` | `"updated"` | Sort by: `updated`, `created`, `harness`, `presence`, `activity`, `cwd`, `id`, `multiplexer`, `tmux`, `presence-changed`, `activity-changed` |
| `--desc` | `bool` | `false` | Sort in descending order |
| `--full` | `bool` | `false` | Show complete values using an adaptive terminal layout |
| `--summary` | `bool` | `false` | Output aggregated session counts |
| `--group-by <field>` | `string` | `""` | Group summaries by: `multiplexer-session`, `project`, `harness` (requires `--summary`) |
| `--project <path>` | `string` | `""` | Match project root |
| `--project-subtree` | `bool` | `false` | Include project roots or working directories below `--project` |
| `--cwd <path>` | `string` | `""` | Match working directory |
| `--multiplexer <kind>` | `string` | `""` | Filter by `tmux`, `zellij`, or `herdr` |
| `--server <id>` | `string` | `""` | Qualify the multiplexer server/socket |
| `--pane <id>` | `string` | `""` | Filter by pane ID |
| `--absolute-time` | `bool` | `false` | Display absolute timestamps rather than relative times |

**Examples:**

```sh
# List all active sessions
aht list --presence live

# List sessions sorted by creation date descending
aht list --sort created --desc

# Output active sessions in JSON format
aht list --presence live --json

# Show aggregate session count summary (defaults to multiplexer session)
aht list --summary

# Show aggregate session count summary by project
aht list --summary --group-by project

# Show aggregate session count summary by harness
aht list --summary --group-by harness
```

---

### `aht watch`

Stream realtime session updates as agents start, transition, or terminate.

```sh
aht watch [flags]
```

**Flags:**

| Flag | Type | Default | Description |
|---|---|---|---|
| `--presence <val>` | `string` | `"live"` | Filter sessions by presence (`live`, `gone`, `unknown`, `all`) |
| `--activity <val>` | `string` | `""` | Filter by reported activity |
| `--agent <name>` | `string` | `""` | Filter by harness name |
| `--tmux-session <name>` | `string` | `""` | Filter by tmux session |
| `--multiplexer-session <name>` | `string` | `""` | Filter by multiplexer session |
| `--no-snapshot` | `bool` | `false` | Start with future changes only |
| `--format <type>` | `string` | `"table"` | Output format: `table` or `plain` |

The project, working-directory, multiplexer, server, and pane filters from `list`
are also available on `watch`.

When paired with `--json`, `aht watch` emits JSON Lines containing incremental state snapshots.

---

### `aht wait`

Wait for an observed session condition without controlling the agent:

```sh
aht wait <session> --activity idle --timeout 2m --stable-for 1s
aht wait <session> --presence gone --timeout 30s
```

Specify `--activity`, `--presence`, or both. Conditions are combined; `gone` and
`unknown` presence cannot be combined with activity. A zero `--timeout` waits
until cancellation. `--stable-for` requires the condition to persist across
observed snapshots and resets if the process incarnation changes. It cannot
prove that an unobserved transition did not occur.

Success prints the matching session (`--json` returns the session object).
Timeout, missing session, premature disappearance, unknown presence, and broker
errors fail with exit code 1; invalid conditions or durations use exit code 2.
Auto mode can fall back from a broker subscription to watching the durable file.

### `aht current`

Resolve the session containing the calling process, using verified process ancestry
and terminal evidence. `--json` returns the session object. Missing, stale, or
ambiguous evidence produces an error rather than guessing.

### `aht info`

Display detailed metadata and diagnostics for a specific session.

```sh
aht info <session-id> [flags]
aht info --pane <pane-id> [flags]
```

**Flags:**

| Flag | Type | Description |
|---|---|---|
| `--explain` | `bool` | Explain screen inspection heuristics and activity state derivation |
| `--pane <id>` | `string` | Look up session by terminal multiplexer pane ID (e.g. `%0` in tmux) |
| `--server <id>` | `string` | Qualify `--pane` by server/socket |
| `--multiplexer <kind>` | `string` | Qualify `--pane` by multiplexer kind |
| `--config-dir <path>` | `string` | Custom directory containing detection manifest files (requires `--explain`) |
| `--screen-inspection` | `bool` | Enable/disable terminal multiplexer screen inspection (default: true) |

**Examples:**

```sh
# Inspect a specific session
aht info codex-session-abc

# Explain how activity was detected from the terminal pane
aht info codex-session-abc --explain

# Inspect whatever session is running in tmux pane %2
aht info --pane %2
```

---

### `aht stop`

Gracefully shut down running agent sessions.

```sh
aht stop [session-id...] [flags]
```

**Flags:**

| Flag | Type | Description |
|---|---|---|
| `-a, --all` | `bool` | Target every currently live session |
| `-n, --dry-run` | `bool` | Preview targeted sessions and signals without sending them |
| `-y, --yes` | `bool` | Confirm stopping all sessions without interactive prompt |

**Examples:**

```sh
# Stop a specific session
aht stop codex-session-abc

# Stop all running sessions with confirmation prompt (requires TTY)
aht stop --all

# Non-interactive stop in scripts / CI
aht stop --all --yes

# Preview sessions that would be stopped
aht stop --all --dry-run
```


## Management Commands (`aht manage`)

Subcommands under `aht manage` configure integrations, the background tracker service, state storage, and diagnostics.

### `aht manage setup`

Install integrations and enable the background tracker in one step. Requires at least one harness name or `all`.

```sh
aht manage setup <agent... | all> [flags]
```

**Flags:**

| Flag | Type | Description |
|---|---|---|
| `--binary <path>` | `string` | Custom `aht` executable path to bind |
| `-n, --dry-run` | `bool` | Show intended operations without writing changes |
| `-f, --force` | `bool` | Overwrite existing foreign integration files |

---

### `aht manage upgrade`

Refresh already-installed AHT integrations and the managed tracker after replacing the binary:

```sh
aht manage upgrade [-n, --dry-run]
```

---

### `aht manage integrations`

Manage lifecycle hooks and extensions across supported coding-agent harnesses.

```sh
aht manage integrations <command>
```

**Subcommands:**

- `install <agent... | all>`: Install hooks and extensions.
  - `--binary <path>`: Executable path bound to integrations.
  - `--target-binary <path>`: Real agent binary path for wrapper shims (requires `--shim`).
  - `--shim`: Install PATH shim instead of native hooks.
  - `-n, --dry-run`: Show intended changes without writing.
  - `-f, --force`: Overwrite existing files.
  - `--show-content`: Display generated file content during dry-run.
- `remove <agent... | all>`: Remove installed hooks and extensions.
  - `-n, --dry-run`: Preview removals.
- `status [agent...]`: Inspect current installation health and versions.
  - `--binary <path>`: Expected binary path to verify.

---

### `aht manage tracker`

Control the background reconciliation observer daemon.

```sh
aht manage tracker <command>
```

**Subcommands:**

- `enable`: Install, configure, and start the background service (`systemd` on Linux, `launchd` on macOS).
  - `--interval <duration>`: Reconciliation interval (default: 300ms).
  - `--grace-period <duration>`: Absence grace period (default: 0s).
  - `-n, --dry-run`: Preview service registration without writing.
- `disable`: Stop and disable the background service (`-n, --dry-run` supported).
- `status`: Check if the background tracker service is active and healthy.
- `run`: Run reconciliation in the foreground (for containers or debugging).
  - `--interval <duration>`: Reconciliation interval.
  - `--grace-period <duration>`: Absence grace period.
  - `-q, --quiet`: Suppress routine cycle output.
  - `--once`: Run one reconciliation cycle and exit.
  - `--auto-clean`: Automatically clean expired gone sessions.
  - `--screen-inspection`: Enable/disable multiplexer screen inspection.

---

### `aht manage state`

Inspect and maintain the durable registry state file (`state.json`).

```sh
aht manage state <command>
```

**Subcommands:**

- `path`: Print the path to the active `state.json` registry file.
- `clean`: Garbage-collect expired session tombstones.
  - `-a, --all`: Purge all gone session records immediately (requires `-y, --yes` in non-interactive environments).
  - `--older-than <duration>`: Purge gone sessions older than the specified age (e.g. `24h`, `7d`).
  - *If neither flag is passed, the `retention.max_gone_age` setting from `config.toml` (default `7d`) is used.*
- `reset`: Reset and clear the session registry.
  - `-f, --force`: Required flag to confirm destructive state reset.

---

### `aht manage doctor`

Run comprehensive diagnostic checks to validate harness integrations, configuration, tracker service health, and terminal multiplexer access.

```sh
aht manage doctor [flags]
```

**Flags:**

| Flag | Type | Description |
|---|---|---|
| `-v, --verbose` | `bool` | Include details for uninstalled integrations and all harness capabilities |

Supports `--json` for automated health auditing.

---

### `aht manage config`

Inspect and manage configuration settings.

```sh
aht manage config <command>
```

**Subcommands:**

- `path`: Print the resolved path to the active configuration file.
- `show`: Print the effective configuration (merging defaults, discovered files, and environment variables).
  - Supports `--json` to output parsed JSON instead of TOML.
- `init`: Generate the default configuration file if missing.
  - `-f, --force`: Overwrite existing configuration file with the default template.
  - `--json`: Output result as JSON (`{"created": true, "path": "..."}`).
  - When `--config -` is supplied, writes the default template directly to stdout.

---

### `aht manage capabilities`

List the static capabilities of all supported harnesses, or inspect one:

```sh
aht manage capabilities
aht manage capabilities --harness codex --json
```

Capabilities describe what an adapter supports, including lifecycle evidence,
screen detection, authority, and resume metadata. They do not indicate whether
an integration is installed or currently reporting. Use `manage integrations
status` and `manage doctor` for local installation and tracker health.

### `aht manage detection`

Inspect and test agent state detection rules offline against saved terminal screen fixtures without requiring a running agent, tracker daemon, tmux session, or installed harness.

```sh
aht manage detection <command>
```

**Subcommands:**

- `test <harness>`: Test detection manifests and explain rule decisions against a screen fixture.

#### `aht manage detection test`

```sh
aht manage detection test <harness> --screen <path|-> [flags]
```

**Flags:**

| Flag | Type | Description |
|---|---|---|
| `--screen <path>` | `string` | Saved terminal screen fixture `<path>` or `-` for stdin (**required**) |
| `--manifest <path>` | `string` | Explicit detection manifest `<path>` (fails on any error without fallback) |
| `--config-dir <dir>` | `string` | Detection manifest override directory (preserves ambient fallback behavior) |
| `--title <string>` | `string` | Terminal window or pane `<title>` |
| `--show-screen` | `bool` | Include normalized screen text in output (default `false`) |

Supports global `--json` for structured automated verification.

**Fixture-Driven Rule Authoring Example:**

1. **Capture a terminal screen fixture**:
   Capture an actual terminal state from a tmux pane or save sample output to a fixture file:

   ```sh
   # Save live pane text to a fixture file
   tmux capture-pane -p -t %0 > fixtures/waiting_approval.txt
   ```

2. **Draft or customize a detection manifest** (`custom-codex.toml`):

   ```toml
   version = 1
   agent = "codex"

   [[rules]]
   id = "permission_prompt"
   state = "waiting"
   priority = 90
   region = "bottom:5"
   any = [
     "Would you like to run the following command?",
     "Do you want to proceed?",
   ]

   [[rules]]
   id = "working_interruptible"
   state = "running"
   priority = 80
   region = "bottom:2"
   regex_any = [
     "Thinking… esc to interrupt",
     "Running command · esc to interrupt",
   ]
   ```

3. **Test offline against the saved fixture**:

   ```sh
   aht manage detection test codex \
     --manifest custom-codex.toml \
     --screen fixtures/waiting_approval.txt
   ```

   **Human Output Example:**

   ```text
   Harness:             codex
   Manifest source:     custom-codex.toml
   Manifest version:    1
   Lines evaluated:     15
   Effective activity:  waiting
   Reason:              manifest_rule
   Winning rule:        permission_prompt

   Rule                   State     Priority  Region    Match   Reason
   ───────────────────────────────────────────────────────────────────────────────────────
   permission_prompt      waiting         90  bottom:5  winner  matched
   working_interruptible  running         80  bottom:2  no      regex_any: none of 2 regular expressions matched
   ```

4. **Verify machine-readable output in CI or test suites**:

   ```sh
   aht manage detection test codex \
     --manifest custom-codex.toml \
     --screen fixtures/waiting_approval.txt \
     --json
   ```

   Pipe directly from stdin:

   ```sh
   cat fixtures/waiting_approval.txt | aht manage detection test codex --screen - --json
   ```

---

## Shell Completion (`aht completion`)

AHT provides native shell completion generation:

```sh
# Bash (.bashrc)
source <(aht completion bash)

# Zsh (.zshrc)
source <(aht completion zsh)

# Fish
aht completion fish > ~/.config/fish/completions/aht.fish

# PowerShell
aht completion powershell >> $PROFILE
```

---

## Integration Protocol Endpoints

These endpoints are machine protocol interfaces used by lifecycle hooks, IDEs, and external integrations; they are not intended for interactive manual use.

### `aht hook`

Integration protocol endpoint for native request/response hooks and streaming proxies:

- `aht hook <harness> --event <name>`: Respond to two-way native lifecycle hooks (requires `--json`).
- `aht hook wire kimi-code -- [native Kimi options]`: Run an owned Kimi Code process using its native Wire protocol with tracked approval waiting over stdio.

All arguments following the `--` delimiter in `aht hook wire` are forwarded byte-for-byte to the Kimi Wire process.

### `aht report`

Record a one-way harness observation (presence, activity, lifecycle, identity) directly to the registry:

```sh
aht report <harness> [flags]
```

Invoked by managed shell hooks and integration scripts on lifecycle state transitions.
