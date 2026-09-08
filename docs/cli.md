# CLI Reference

The `aht` command-line tool tracks, inspects, and manages local coding-agent sessions.

## Global Flags

These flags apply to all commands:

| Flag | Type | Description |
|---|---|---|
| `--config <path>` | `string` | Explicit configuration file path (skips auto-creation) |
| `--store <path>` | `string` | Registry state file path (defaults to `~/.local/state/aht/sessions.json`) |
| `--json` | `bool` | Emit JSON output (JSON Lines for streaming commands) |
| `-v, --version` | `bool` | Print version, commit hash, and build timestamp |
| `-h, --help` | `bool` | Help for `aht` or any subcommand |

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
| `-p, --presence` | `string` | `"all"` | Filter sessions by presence: `live`, `gone`, `unknown`, `all` |
| `-s, --sort` | `string` | `"updated"` | Sort by: `updated`, `created`, `harness`, `presence`, `activity`, `cwd`, `id`, `multiplexer`, `tmux` |
| `--desc` | `bool` | `false` | Sort in descending order |
| `-a, --agent` | `string` | `""` | Filter by harness name (un-hides harnesses in `ignore_harnesses`) |
| `--full` | `bool` | `false` | Show complete values using an adaptive terminal layout |
| `--summary` | `bool` | `false` | Output aggregated session counts by harness and presence |
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
| `-p, --presence` | `string` | `"live"` | Filter sessions by presence (`live`, `gone`, `unknown`, `all`) |
| `-a, --agent` | `string` | `""` | Filter by harness name |
| `--plain` | `bool` | `false` | Use plain appended lines instead of a redrawn terminal table |
| `--debounce` | `duration` | `100ms` | Event batching debounce interval |

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
| `-e, --explain` | `bool` | Explain screen inspection heuristics and activity state derivation |
| `--pane <id>` | `string` | Look up session by terminal multiplexer pane ID (e.g. `%0` in tmux) |
| `--config-dir <path>` | `string` | Custom directory containing detection manifest files (requires `--explain`) |

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
| `--all` | `bool` | Target every currently live session |
| `-y, --yes` | `bool` | Skip interactive confirmation when stopping all sessions |
| `--dry-run` | `bool` | Preview targeted sessions and signals without sending them |

**Examples:**

```sh
# Stop a specific session
aht stop codex-session-abc

# Stop all running sessions with confirmation prompt
aht stop --all

# Preview sessions that would be stopped
aht stop --all --dry-run
```

---

## Management Commands (`aht manage`)

Subcommands under `aht manage` configure integrations, the background tracker service, state storage, and diagnostics.

### `aht manage setup`

Convenience command to install integrations and enable the background tracker in one step.

```sh
aht manage setup [harness...]
```

If no harness names are provided, integrations for all supported harnesses are installed.

---

### `aht manage upgrade`

Refresh already-installed AHT integrations and the managed tracker after replacing
the binary:

```sh
aht manage upgrade
aht manage upgrade --dry-run
```

---

### `aht manage integrations`

Manage lifecycle hooks and extensions across supported coding-agent harnesses.

```sh
aht manage integrations [command]
```

**Subcommands:**

- `install [harness... | all]`: Install hooks and extensions.
- `remove [harness... | all]`: Remove installed hooks and extensions.
- `status [harness... | all]`: Inspect current installation health and versions.

**Flags for `install`:**

| Flag | Type | Description |
|---|---|---|
| `--dry-run` | `bool` | Show intended file operations without making changes |
| `--show-content` | `bool` | Display generated hook/extension code during dry run |
| `--target-binary <path>` | `string` | Target binary path for PATH shims (requires `--shim`) |
| `--shim` | `bool` | Install a PATH wrapper shim instead of native hooks |
| `-f, --force` | `bool` | Reinstall and overwrite existing integrations |

---

### `aht manage tracker`

Control the background reconciliation observer daemon.

```sh
aht manage tracker [command]
```

**Subcommands:**

- `enable`: Install and enable the system background service (`systemd` on Linux, `launchd` on macOS).
- `disable`: Stop and disable the background service.
- `status`: Check if the background tracker service is active and healthy.
- `run`: Run the reconciliation tracker in the foreground (useful for containers or debugging).

**Flags for `tracker run`:**

| Flag | Type | Default | Description |
|---|---|---|---|
| `--interval` | `duration` | `300ms` | Polling and reconciliation interval |
| `--grace-period` | `duration` | `0s` | Absence grace window before marking missing processes gone |
| `-q, --quiet` | `bool` | `false` | Suppress routine cycle logs |

---

### `aht manage state`

Inspect and maintain the durable registry state file.

```sh
aht manage state [command]
```

**Subcommands:**

- `path`: Print the path to the active `sessions.json` registry file.
- `clean`: Garbage-collect expired session tombstones.
  - `--all`: Purge all gone session records immediately.
  - `--older-than <duration>`: Purge gone sessions older than the specified age (e.g. `24h`, `7d`).
  - *If neither flag is passed, the `retention.max_gone_age` setting from `config.toml` is used.*
- `reset`: Reset and clear the session registry.

---

### `aht manage doctor`

Run comprehensive diagnostic checks to validate harness integrations, configuration, tracker service health, and terminal multiplexer access.

```sh
aht manage doctor [flags]
```

Supports `--json` for automated health auditing.

---

### `aht manage config`

Inspect and manage configuration settings.

```sh
aht manage config [command]
```

**Subcommands:**

- `path`: Print the resolved path to the active configuration file.
- `show`: Print the effective configuration (merging defaults, `config.toml`, and environment variables).
  - Supports `--json` to output parsed JSON instead of TOML.
- `init`: Generate the default configuration file if missing.
  - `--force`: Overwrite existing configuration file with the default template.
  - `--json`: Output result as JSON (`{"created": true, "path": "..."}`).
