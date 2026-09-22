import plugin from "./opencode_v2_absent.ts";

let finish;
const finished = new Promise((resolve) => {
  finish = resolve;
});

async function* generateEvents() {
  yield {
    type: "session.status",
    data: {
      sessionID: "missing-v2-session",
      status: { type: "idle" },
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
