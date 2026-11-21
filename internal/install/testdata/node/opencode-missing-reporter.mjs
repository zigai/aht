import plugin from "./opencode_absent.ts";

let finish;
const finished = new Promise((resolve) => {
  finish = resolve;
});

async function* generateEvents() {
  yield {
    type: "session.status",
    data: {
      sessionID: "missing-session",
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
