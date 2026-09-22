import plugin from "./plugin.ts";
if (plugin.id !== "aht-state" || typeof plugin.setup !== "function") {
  throw new Error("unexpected plugin export shape: " + JSON.stringify(plugin));
}

let finish;
const finished = new Promise((resolve) => {
  finish = resolve;
});

async function* generateEvents() {
  yield {
    type: "session.error",
    data: {
      sessionID: "opencode-v2-session",
    },
    prompt: process.env.AHT_TEST_SENSITIVE_SENTINEL,
  };
  yield {
    type: "session.status",
    data: {
      sessionID: "opencode-v2-session",
      status: { type: "idle" },
    },
  };
  yield {
    type: "session.idle",
    data: {
      sessionID: "opencode-v2-session",
    },
  };
  finish();
}

const cleanup = await plugin.setup({
  location: { directory: "/tmp/project", project: { canonical: "/tmp/project" } },
  event: {
    subscribe: () => generateEvents(),
  },
});

await finished;
await cleanup();
