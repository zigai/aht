import plugin from "./opencode_absent.ts";
const runtime = await plugin.server({ directory: "/tmp/project", worktree: "/tmp/project" });
await runtime.event({
  event: {
    type: "session.status",
    sessionID: "missing-session",
    properties: { status: { type: "idle" } },
  },
});
await new Promise((resolve) => setTimeout(resolve, 50));
