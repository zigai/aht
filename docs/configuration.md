# Configuration

AHT can be configured via a TOML configuration file, environment variables, or command-line flags.

## Configuration Resolution & Precedence

AHT applies a strict 6-tier precedence ladder when resolving configuration settings (highest priority wins):

1. **Command-Line Flags** (e.g. `--sort`, `--presence`, `--desc`, `--auto-clean`, `--screen-inspection`)
2. **Environment Variables** (`AHT_<SECTION>_<FIELD>`, e.g. `AHT_UI_SORT`)
3. **Project Configuration File**: `./.aht.toml` (in the current working directory)
4. **User Configuration File**: `~/.config/aht/config.toml` (or `$XDG_CONFIG_HOME/aht/config.toml`)
5. **System Configuration File**: `/etc/xdg/aht/config.toml` (or `$XDG_CONFIG_DIRS/aht/config.toml`; `%ProgramData%/aht/config.toml` on Windows)
6. **Hardcoded Defaults**

### Explicit File & Stdin Override

- Supplying `--config <path>` (or the `$AHT_CONFIG` environment variable) selects **one explicit configuration file** that replaces tiers 3–5 (project, user, and system files).
- If `--config -` is specified, AHT reads a single bounded TOML document (up to 1 MiB) from standard input.
- Missing explicitly specified configuration files fail immediately with an error.

### Disk Bypass (`--no-config`)

- Passing `--no-config` bypasses all disk configuration files and stdin entirely, resolving settings using only explicit flags, environment variables, and built-in defaults.

### Automatic First-Run Creation

When an ordinary configured command (such as `aht list`) is executed and no user configuration file exists, AHT automatically generates a clean, commented default configuration file at `~/.config/aht/config.toml` (or `$AHT_CONFIG` if specified).

- Machine protocol commands (`aht hook`, `aht report`), help, version, completion, dry-runs, and invalid commands never trigger file creation.
- Default settings in the generated file are commented examples, ensuring that creating the file does not mask system or project configuration on subsequent runs.

---

## Configuration Options

| Section | Option | Type | Default | Description | Environment Variable |
|---|---|---|---|---|---|
| `ui` | `default_presence` | `string` | `"all"` | Default session presence filter (`live`, `gone`, `unknown`, `all`) | `AHT_UI_DEFAULT_PRESENCE` |
| `ui` | `sort` | `string` | `"updated"` | Field to sort sessions by (`updated`, `created`, `harness`, `presence`, `activity`, `cwd`, `id`, `multiplexer`, `tmux`, `presence-changed`, `activity-changed`) | `AHT_UI_SORT` |
| `ui` | `sort_desc` | `bool` | `false` | Sort in descending order | `AHT_UI_SORT_DESC` |
| `ui` | `absolute_time` | `bool` | `false` | Render absolute rather than relative timestamps | `AHT_UI_ABSOLUTE_TIME` |
| `ui` | `time_format` | `string` | `"relative"` | Timestamp display format (`relative`, `absolute`, `iso8601`) | `AHT_UI_TIME_FORMAT` |
| `retention` | `auto_clean` | `bool` | `false` | Automatically purge expired gone sessions in background tracker | `AHT_RETENTION_AUTO_CLEAN` |
| `retention` | `max_gone_age` | `string` | `"7d"` | Maximum age of gone sessions before tombstone GC | `AHT_RETENTION_MAX_GONE_AGE` |
| `filter` | `ignore_harnesses` | `[]string` | `[]` | Agent harnesses excluded by default (unless `--agent` given) | `AHT_FILTER_IGNORE_HARNESSES` |
| `filter` | `ignore_paths` | `[]string` | `[]` | Working directory patterns/prefixes to omit | `AHT_FILTER_IGNORE_PATHS` |
| `tracker` | `interval` | `string` | `"300ms"` | Background process reconciliation polling frequency | `AHT_TRACKER_INTERVAL` |
| `tracker` | `grace_period` | `string` | `"0s"` | Process absence grace window before marking gone | `AHT_TRACKER_GRACE_PERIOD` |
| `tracker` | `quiet` | `bool` | `false` | Suppress human cycle output and diagnostics from tracker | `AHT_TRACKER_QUIET` |
| `detection` | `manifests_dir` | `string` | `""` | Directory of custom agent screen detection TOML manifests | `AHT_DETECTION_MANIFESTS_DIR` |
| `detection` | `screen_inspection` | `bool` | `true` | Enable terminal multiplexer screen inspection for state derivation | `AHT_DETECTION_SCREEN_INSPECTION` |

---

## Environment Variable Overrides

Any configuration option can be overridden using an environment variable prefixed with `AHT_` followed by the section and setting name in uppercase:

```sh
# Override sort field and order
export AHT_UI_SORT="created"
export AHT_UI_SORT_DESC="true"

# Override default presence filter
export AHT_UI_DEFAULT_PRESENCE="live"

# Set tracker polling interval
export AHT_TRACKER_INTERVAL="500ms"

# Override configuration file location
export AHT_CONFIG="/etc/aht/config.toml"
```

Array options (such as `AHT_FILTER_IGNORE_HARNESSES` and `AHT_FILTER_IGNORE_PATHS`) accept comma-separated strings:

```sh
export AHT_FILTER_IGNORE_HARNESSES="copilot,cline"
```

An empty string (`export AHT_FILTER_IGNORE_HARNESSES=""`) explicitly clears the list.

---

## Managing Configuration via CLI

AHT provides dedicated commands under `aht manage config` to inspect and manage your configuration:

### Print Configuration Path

Print the resolved path to the active configuration file:

```sh
aht manage config path
# Output: /home/user/.config/aht/config.toml

aht manage config path --json
# Output: {"path":"/home/user/.config/aht/config.toml"}
```

### Show Effective Configuration

Display the effective merged configuration in TOML format:

```sh
aht manage config show
```

Or as JSON:

```sh
aht manage config show --json
```

### Initialize Configuration File

Generate or re-initialize the default configuration file:

```sh
# Initialize if missing
aht manage config init

# Overwrite existing file with default template
aht manage config init --force

# JSON output
aht manage config init --json

# Print default template directly to stdout
aht --config - manage config init
```

---

## Default Configuration Template

Below is the complete default `config.toml` template generated by AHT:

```toml
# AHT Configuration (config.toml)
# Track local coding-agent sessions and where they are running.
# Override location with $AHT_CONFIG or --config <path>.

[ui]
# Default session presence filter: "live", "gone", "unknown", or "all"
default_presence = "all"

# Sort sessions by: "updated", "created", "harness", "presence", "activity", "cwd", "id", "multiplexer", "tmux"
sort = "updated"

# Sort in descending order
sort_desc = false

# Display timestamps in absolute format rather than relative
absolute_time = false

# Timestamp display format: "relative", "absolute", or "iso8601"
time_format = "relative"

[retention]
# Automatically clean up expired gone sessions in background tracker
auto_clean = false

# Maximum age of gone sessions before tombstone cleanup, e.g. "7d", "24h"
max_gone_age = "7d"

[filter]
# List of harnesses to omit from default session listings unless explicitly requested via --agent
ignore_harnesses = []

# List of glob or directory path patterns matching session working directories to ignore
ignore_paths = []

[tracker]
# Reconciliation polling frequency
interval = "300ms"

# Process absence grace period before marking sessions gone
grace_period = "0s"

# Suppress human cycle output and diagnostics from tracker
quiet = false

[detection]
# Directory containing custom agent screen detection manifest TOML files
manifests_dir = ""

# Enable terminal multiplexer screen inspection
screen_inspection = true
```
