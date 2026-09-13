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
| `--summary` | `bool` | `false` | Output aggregated session counts by multiplexer session |
| `--absolute-time` | `bool` | `false` | Display absolute timestamps rather than relative times |

**Examples:**

```sh
# List all active sessions
aht list --presence live

# List sessions sorted by creation date descending
aht list --sort created --desc

# Output active sessions in JSON format
aht list --presence live --json

# Show aggregate session count summary
aht list --summary
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

When paired with `--json`, `aht watch` emits JSON Lines containing incremental state snapshots.

---

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
