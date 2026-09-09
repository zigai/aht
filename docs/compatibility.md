# Harness compatibility checks

Two GitHub Actions workflows exercise the installed harnesses using the same
`TestCurrentHarnessLifecycle` test and reusable `compatibility-host.yml` workflow.
They require no provider credentials: the lifecycle tests use an isolated local
provider. Cursor and Antigravity retain their existing discovery-only coverage.

The CI utility lives in `internal/tools/compatibility` within the existing Go
module. Workflows use `actions/setup-go` and `go run ./internal/tools/compatibility`;
Go compiles and runs the utility on the runner. There are no checked-in binaries,
extra Go modules, or JavaScript tooling dependencies. The utility is covered by
the normal Go tests, race tests, and lint checks. Node is still set up in harness
jobs because npm-distributed harnesses need it.

## Release detection

**Release compatibility** checks public version metadata hourly, at minute 32.
It runs a harness job only when the latest stable distribution enters a newer
major/minor series. Patch-only changes, prereleases, and rollbacks do not trigger
automatic checks. For example, `1.2.3` → `1.2.9` does nothing; `1.2.3` → `1.3.2`
tests exactly `1.3.2`. The first run tests the current stable version of every
release-tracked harness to establish a baseline.

This is lightweight polling, not an upstream webhook subscription. GitHub's
[`release` event](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#release)
only watches the workflow's own repository, and
[npm publication hooks were retired](https://github.blog/changelog/2024-07-16-sunset-notice-npm-hooks-api-endpoints/).
There is no external service, custom token, state branch, or automatic PR.

The catalog is maintained in [catalog.go](../internal/tools/compatibility/catalog.go).
Sources and install paths were checked on 2026-09-09:

| Harness | Version source | Exact installation |
| --- | --- | --- |
| Claude | npm `@anthropic-ai/claude-code`, `latest` | `npm install --global @anthropic-ai/claude-code@VERSION` |
| Codex | npm `@openai/codex`, `latest` | `npm install --global @openai/codex@VERSION` |
| Copilot | npm `@github/copilot`, `latest` | `npm install --global @github/copilot@VERSION` |
| Cline | npm `cline`, `latest` | `npm install --global cline@VERSION` |
| Pi | npm `@earendil-works/pi-coding-agent`, `latest` | `npm install --global @earendil-works/pi-coding-agent@VERSION` |
| OMP | npm `@oh-my-pi/pi-coding-agent`, `latest` | `npm install --global @oh-my-pi/pi-coding-agent@VERSION` |
| OpenCode | npm `opencode-ai`, `latest` | `npm install --global opencode-ai@VERSION` |
| Kilo | npm `@kilocode/cli`, `latest` | `npm install --global @kilocode/cli@VERSION` |
| Droid | npm `droid`, `latest` | `npm install --global droid@VERSION` |
| OpenClaw | npm `openclaw`, `latest` | `npm install --global openclaw@VERSION` |
| Kimi Code | PyPI `kimi-cli` | `uv tool install kimi-cli==VERSION` |
| Hermes | PyPI `hermes-agent` | `uv tool install hermes-agent==VERSION` |
| Goose | GitHub `aaif-goose/goose`, latest stable release | Release's `download_cli.sh` with `GOOSE_VERSION=TAG` and `CONFIGURE=false` |
| Antigravity | GitHub `google-antigravity/antigravity-cli`, latest stable release | Release's `agy_cli_linux_x64.tar.gz`; install its `antigravity` binary as `agy` |
| Grok | Official `https://x.ai/cli/stable` channel | Official installer with the explicit version argument |

The package sources use the [npm registry](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md)
and [PyPI JSON API](https://docs.pypi.org/api/json/), with
[exact npm versions](https://docs.npmjs.com/cli/v11/commands/npm-install/) and
[uv tool installation](https://docs.astral.sh/uv/guides/tools/#installing-tools).
Droid's npm distribution is documented in its
[official quickstart](https://docs.factory.ai/cli/getting-started/quickstart).
Goose's [official installer](https://github.com/aaif-goose/goose/blob/main/download_cli.sh)
documents `GOOSE_VERSION`. Antigravity publishes its native binaries as
[GitHub release assets](https://github.com/google-antigravity/antigravity-cli/releases).
Grok's [official installer](https://x.ai/cli/install.sh) documents the version
argument and uses the public stable-channel endpoint directly. The detector
reads that endpoint; it does not parse installer code or scrape release pages.

Detection follows the latest **packaged distribution**, not every source commit
or GitHub tag. It samples the latest stable channel, so multiple intervening
minor releases between polls are coalesced into a check of the newest one.
Calendar versions follow the same numeric rule: for OpenClaw `2026.9.3`, a change
to the third component alone is excluded. The lifecycle log must report the
requested version before a pinned check can pass.

## Weekly fallback

**Compatibility** retains the Monday 06:17 UTC schedule for **Cursor**, plus the
existing Zellij and Herdr checks. Release-tracked harnesses are excluded from
this workflow.

Cursor's [official CLI installation docs](https://cursor.com/docs/cli/installation)
offer an updating installer. The current installer embeds a dated build ID
rather than exposing a documented major/minor release feed with an exact-version
installation contract. Cursor stays on the existing install-and-test path;
the workflow does not scrape the installer to infer versions.

## State, failures, and manual checks

Each completed release check records the exact version, source, outcome, and run
URL. Both passing and failing checks advance the major/minor baseline, preventing
the same failure from running hourly. Missing or canceled results remain eligible
for retry; a failed metadata request leaves that harness's baseline intact and
does not stop other harnesses from being tested. Failures are visible in workflow
results, summaries, and diagnostics.

The workflow serializes detection, tests, and state publication. State is a small
GitHub Actions artifact, `compatibility-release-state-v1`, renewed each poll with
90-day retention (subject to repository retention limits). It is restored only
from this workflow's scheduled/manual runs on the default branch. API errors or
invalid state fail visibly rather than silently resetting the baseline. If all
state artifacts are deleted or expire while the workflow is inactive, the next
run establishes a fresh baseline. A rerun of an older workflow cannot replace
newer recorded versions. No repository write permissions are needed.

To retry a failure or check after an AHT change, manually run **Release
compatibility** on the default branch, set `harness` to its catalog ID (or `all`),
and enable `force`. This tests the current stable version even within an already
checked series. To test Cursor, manually run **Compatibility**. A manual release
run without `force` just performs normal detection.

Local commands:

```sh
just compatibility-tests                  # Offline detector/workflow regression tests
go run ./internal/tools/compatibility probe  # Read-only query of all release sources
just compatibility codex                 # Lifecycle test of an already installed harness
```
