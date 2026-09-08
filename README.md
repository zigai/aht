# AHT

[![CI](https://github.com/zigai/aht/actions/workflows/ci.yml/badge.svg)](https://github.com/zigai/aht/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/zigai/aht/pkg/client.svg)](https://pkg.go.dev/github.com/zigai/aht/pkg/client)
[![Go version](https://img.shields.io/github/go-mod/go-version/zigai/aht)](https://github.com/zigai/aht/blob/master/go.mod)
[![Release](https://img.shields.io/github/v/release/zigai/aht)](https://github.com/zigai/aht/releases/latest)
[![License: MIT](https://img.shields.io/github/license/zigai/aht)](https://github.com/zigai/aht/blob/master/LICENSE)

AHT (Agent Harness Tracker) tracks coding-agent sessions, activity, processes,
and terminal-multiplexer locations through a local realtime broker.

Supported harnesses: [`claude`](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code/overview),
[`codex`](https://github.com/openai/codex), [`pi`](https://github.com/earendil-works/pi),
[`opencode`](https://github.com/anomalyco/opencode), [`omp`](https://github.com/can1357/oh-my-pi),
[`hermes`](https://github.com/NousResearch/hermes-agent),
[`openclaw`](https://github.com/openclaw/openclaw), [`grok`](https://x.ai),
[`cursor`](https://cursor.com), [`copilot`](https://github.com/features/copilot),
[`cline`](https://github.com/cline/cline), [`kimi-code`](https://kimi.moonshot.cn),
[`goose`](https://github.com/block/goose),
[`agy`](https://github.com/google-antigravity/antigravity-cli),
[`kilo`](https://github.com/kilo-org/kilocode),
[`droid`](https://factory.ai).

## Installation

```sh
go install github.com/zigai/aht@latest
```

Prebuilt archives and Linux packages are available from
[GitHub Releases](https://github.com/zigai/aht/releases/latest).

## Quick start

Set up harness integrations and start background tracking:

```sh
aht manage setup claude codex
```

View sessions:

```sh
aht list
aht watch
aht info <session>
aht info <session> --explain
```

Check the installation:

```sh
aht manage doctor
```

## Documentation

- [Configuration](docs/configuration.md) — Configuration file (`config.toml`), options reference, and environment overrides
- [CLI Reference](docs/cli.md) — Command-line interface usage, commands, and flags
- [Go Library](docs/library.md) — Package guide, client API, and Go integration examples

## Hook Installation

```sh
aht manage integrations install <harness>
aht manage integrations install all
aht manage integrations install codex --dry-run --show-content
```

`<harness>` is a supported harness name from the list above.

## License

[MIT](https://github.com/zigai/aht/blob/master/LICENSE)
