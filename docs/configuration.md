# Configuration

AHT works without configuration. To inspect or edit settings:

```sh
aht manage config path
aht manage config show
aht manage config get ui.sort
aht manage config set ui.sort created
aht manage config show --provenance
aht manage config schema
```

The configuration file is `~/.config/aht/config.toml`.

## Settings

Set these options in the TOML file or with an available command flag.

| Setting | Description | Default |
| --- | --- | --- |
| `ui.default_presence` | Default session filter: `live`, `gone`, `unknown`, or `all`. | `"all"` |
| `ui.sort` | Sort by `updated`, `created`, `harness`, `presence`, `activity`, `cwd`, `id`, `multiplexer`, `tmux`, `presence-changed`, or `activity-changed`. | `"updated"` |
| `ui.sort_desc` | Sort in descending order. | `false` |
| `ui.absolute_time` | Show absolute timestamps instead of relative times. | `false` |
| `ui.time_format` | Timestamp mode: `relative`, `absolute`, or `iso8601`. The latter two enable absolute timestamps. | `"relative"` |
| `retention.auto_clean` | Automatically remove expired records of gone sessions in the tracker. | `false` |
| `retention.max_gone_age` | Age threshold for removing gone-session records, also used by `manage state clean`. | `"7d"` |
| `filter.ignore_harnesses` | Hide these harnesses unless selected with `--agent` or `--source`. | `[]` |
| `filter.ignore_paths` | Ignore sessions matching directory paths or glob patterns. | `[]` |
| `tracker.interval` | Time between tracker reconciliation cycles; must be positive. | `"300ms"` |
| `tracker.grace_period` | Wait this long after a process disappears before marking it gone. | `"0s"` |
| `tracker.quiet` | Suppress routine tracker cycle output and diagnostics. | `false` |
| `detection.manifests_dir` | Custom detection TOML directory; empty checks `aht/detection` under the user configuration directory before built-in rules. | `""` |
| `detection.screen_inspection` | Inspect terminal panes to help determine agent activity. | `true` |

Durations accept units such as `ms`, `s`, `m`, `h`, and whole days (`7d`).

## Default configuration

```toml
[ui]
default_presence = "all"
sort = "updated"
sort_desc = false
absolute_time = false
time_format = "relative"

[retention]
auto_clean = false
max_gone_age = "7d"

[filter]
ignore_harnesses = []
ignore_paths = []

[tracker]
interval = "300ms"
grace_period = "0s"
quiet = false

[detection]
manifests_dir = ""
screen_inspection = true
```

## Environment

These path overrides are separate from the TOML settings above.

| Setting | Description | Default | Environment variable |
| --- | --- | --- | --- |
| Configuration file | Select one file instead of discovered files; `--config` takes precedence. | User-file location shown above. | `AHT_CONFIG` |
| State directory | Directory containing registry state. | `$XDG_STATE_HOME/aht`, otherwise `~/.local/state/aht`. | `AHT_STATE_DIR` |
| Registry file | Exact registry file; `--store` takes precedence. | `state.json` in the state directory. | `AHT_STORE` |
| Broker socket | Socket used to connect to the tracker. | Registry file path with `.sock` appended. | `AHT_SOCKET` |

## Reporting and durable state

Generated integrations identify their reporter with typed flags:

```sh
aht report pi --session-id SESSION_ID --event session_start \
  --reporter pi-extension --reporter-version 16 --sequence 1
```

`--reporter` names the integration, `--reporter-version` identifies its generated
artifact version, and `--sequence` orders that reporter's observations. Add
`--multi-session` for a reporter that manages multiple native identities in one
process. Ordinary native metadata still uses `--attribute key=value`.

Run `aht manage integrations status` to find stale artifacts and
`aht manage integrations upgrade` to replace them with current managed files.

State consists of the registry snapshot and `<store>.journal.jsonl`. The journal
is fsynced and limited to 64 MiB; exceeding the limit returns an error. The tracker
owns live state and applies pending journal entries before operations. When no
tracker owns the state, a writer folds the snapshot and journal under the store
lock and checkpoints the result. Reads without a broker fold both files, so they
include pending writes. Clean and reset use the same ordered command stream.
Snapshots settle after 25 ms, with a 250 ms maximum batching interval, and flush
on tracker shutdown.

Snapshots use schema 3; other versions are rejected with instructions to reset
the state. Restart the tracker after upgrading the binary. Keep snapshot and journal together when
moving or backing up an instance while writes may be pending.

Session JSON from `list --json` and `info` contains
`liveness: {"kind":"live","activity":"idle"}` (optionally with a decision), or
`{"kind":"gone","at":"...","reason":"..."}`. Terminal context is under
`location`, with `kind` such as `tmux`, `zellij`, or `herdr`. Watch events
retain presence/activity change fields and use `location` for their terminal
label. `--tmux-session` is accepted as an alias for the multiplexer-session filter.
