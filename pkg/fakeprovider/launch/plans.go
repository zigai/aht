package launch

import "github.com/zigai/aht/v2/pkg/registry"

const modelFlag = "--model"

var plans = map[registry.Harness]func(Paths, Model) (Plan, error){
	registry.Harness("claude"):    claudePlan,
	registry.Harness("codex"):     codexPlan,
	registry.Harness("cline"):     clinePlan,
	registry.Harness("copilot"):   copilotPlan,
	registry.Harness("droid"):     droidPlan,
	registry.Harness("goose"):     goosePlan,
	registry.Harness("grok"):      grokPlan,
	registry.Harness("hermes"):    hermesPlan,
	registry.Harness("kilo"):      kiloPlan,
	registry.Harness("kimi-code"): kimiPlan,
	registry.Harness("omp"):       ompPlan,
	registry.Harness("openclaw"):  openclawPlan,
	registry.Harness("opencode"):  opencodePlan,
	registry.Harness("pi"):        piPlan,
	registry.Harness("qwen"):      qwenPlan,
}

func Supported(id registry.Harness) bool {
	_, ok := plans[id]
	return ok
}
