import plugin from "./plugin.ts";
if (plugin.id !== "aht-state" || typeof plugin.server !== "function") {
  throw new Error("unexpected plugin export shape: " + JSON.stringify(plugin));
}
const runtime = await plugin.server({ directory: "/tmp/project", worktree: "/tmp/project" });
if (typeof runtime.event !== "function") {
  throw new Error("unexpected plugin runtime hooks: " + JSON.stringify(runtime));
}
await runtime.event({
  event: {
    type: "session.error",
    sessionID: "kilo-session",
    prompt: process.env.AHT_TEST_SENSITIVE_SENTINEL,
  },
});
await runtime.event({
  event: {
    type: "session.status",
    sessionID: "kilo-session",
    properties: { status: { type: "idle" } },
  },
});
await runtime.event({ event: { type: "session.idle", sessionID: "kilo-session" } });
