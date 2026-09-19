# AHT, sesh, and agent

AHT owns discovering, identifying, tracking, and describing coding-agent sessions.
Its CLI and public Go packages expose the same core behavior. Consumers should
use these packages rather than importing `internal`, parsing table output, or
reimplementing harness detection.

| Project | Responsibility | Examples |
| --- | --- | --- |
| AHT | Session information and observation | Identity, presence, activity, location, selectors, waits, summaries, diagnostics, harness capabilities |
| sesh | Terminal workspace interaction | tmux layouts, session/pane navigation, focus, workspace restoration, agent-aware pickers |
| agent | Agent control | Launch, resume, prompts, interruption, task orchestration, SSH, native control protocols |

Both sesh and the planned Go rewrite of agent should consume AHT's public API.
AHT's `Session` already carries native session identity, resume arguments,
working directory, process identity, and multiplexer location. A consumer can
use those facts to act without putting the action into AHT.

For example, AHT resolves a session and reports its pane; sesh focuses that pane.
AHT returns a recorded session's resume metadata; agent decides how and where to
launch it. AHT can wait for observed activity to become idle; agent owns what
happens next. AHT installation and tracker-service management remain part of
making its own tracking work.

The existing `aht stop` command remains for compatibility. This integration does
not expand it into a public orchestration API or add focus/resume commands.
Control code must revalidate process and pane identity immediately before acting;
a previously read AHT snapshot is not proof that the target is still the same.

## Historical session search

Unified search across harness conversation histories belongs in AHT's discovery
role. It is a future feature, not implemented by the feature integration here.
It should expose a public Go API as well as a CLI, with native session references,
project metadata, and bounded matching excerpts suitable for other tools.

A historical catalog must be separate from the live registry and its short-lived
gone-session tombstones. It should discover existing harness archives even when
AHT was not running when they were created. An activity-decision journal is also
different from conversation history: it explains tracking decisions, not what
an agent discussed. Opening or resuming a search result belongs to the consumer.
