# AHT

AHT (Agent Harness Tracker) gives you one local view of your coding-agent
sessions. See what agents are doing, find their terminal panes, and search
retained conversations across harnesses.

[![CI](https://github.com/zigai/aht/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/zigai/aht/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/zigai/aht?color=blue)](https://github.com/zigai/aht/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/zigai/aht/pkg/aht.svg)](https://pkg.go.dev/github.com/zigai/aht/pkg/aht)
[![Go version](https://img.shields.io/github/go-mod/go-version/zigai/aht)](https://github.com/zigai/aht/blob/master/go.mod)
[![License: MIT](https://img.shields.io/github/license/zigai/aht)](https://github.com/zigai/aht/blob/master/LICENSE)

- **Live tracking:** Follow session activity and locations across tmux, Zellij, and herdr.
- **Conversation search:** Find retained discussions, including sessions created before AHT was installed.
- **Go API and JSON output:** Build tools that query session state and subscribe to changes.

Supported harnesses: [`claude`](https://code.claude.com/docs/en/overview),
[`codex`](https://github.com/openai/codex), [`pi`](https://github.com/earendil-works/pi),
[`opencode`](https://github.com/anomalyco/opencode), [`omp`](https://github.com/can1357/oh-my-pi),
[`hermes`](https://github.com/NousResearch/hermes-agent),
[`openclaw`](https://github.com/openclaw/openclaw), [`grok`](https://github.com/superagent-ai/grok-cli),
[`cursor`](https://cursor.com), [`copilot`](https://github.com/features/copilot),
[`cline`](https://github.com/cline/cline), [`kimi-code`](https://github.com/MoonshotAI/kimi-cli),
[`goose`](https://github.com/block/goose),
[`agy`](https://github.com/google-antigravity/antigravity-cli),
[`kilo`](https://github.com/kilo-org/kilocode),
[`droid`](https://factory.ai),
[`amp`](https://ampcode.com).

## Installation

```sh
go install github.com/zigai/aht@latest
```

Supports Linux and macOS. Building from source requires Go 1.27.1 or newer.

## Quick start

Install your chosen harnesses, then set up tracking:

```sh
aht manage setup claude codex
```

Start a harness session, then list sessions or follow changes:

```sh
aht list
aht watch
```

Search retained conversations, including sessions created before AHT was installed:

```sh
aht search "refresh token" --dir /work/app
```

## Documentation

- [Configuration](docs/configuration.md)
- [Go library](docs/library.md)

## Hook Installation

```sh
aht manage integrations install <harness>
aht manage integrations install all
aht manage integrations install codex --dry-run --show-content
```

`<harness>` is a supported harness name from the list above.

## License

[MIT](LICENSE)
