package registry

const (
	HarnessClaude   Harness = "claude"
	HarnessCodex    Harness = "codex"
	HarnessCursor   Harness = "cursor"
	HarnessCopilot  Harness = "copilot"
	HarnessCline    Harness = "cline"
	HarnessKimiCode Harness = "kimi-code"
	HarnessGrok     Harness = "grok"
	HarnessGoose    Harness = "goose"
	HarnessPi       Harness = "pi"
	HarnessOmp      Harness = "omp"
	HarnessOpenCode Harness = "opencode"
	HarnessAgy      Harness = "agy"
	HarnessKilo     Harness = "kilo"
	HarnessDroid    Harness = "droid"
	HarnessOpenClaw Harness = "openclaw"
	HarnessHermes   Harness = "hermes"
	HarnessAmp      Harness = "amp"
)

type fixtureRules struct{ authority Authority }

func (fixtureRules) Known(id Harness) bool {
	return id != "" && id != "invalid" && id != "unknown" && id != "not-a-harness" && id != "claude-code"
}

func (r fixtureRules) Policy(id Harness) Policy {
	authority := r.authority
	if authority == "" {
		authority = AuthorityHook
	}
	reporter := string(id) + "-extension"
	return Policy{Reporter: reporter, ExclusiveProcess: id != HarnessOpenClaw, Authority: authority, ScreenFallback: true, CatalogCreates: id == HarnessClaude}
}
