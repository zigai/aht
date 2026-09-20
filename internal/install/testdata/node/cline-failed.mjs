import plugin from "./index.js";
const hooks = plugin.hooks;
plugin.setup(
  {},
  { session: { sessionId: "cline-session" }, workspaceInfo: { rootPath: "/tmp/project" } },
);
await hooks.afterRun({
  snapshot: { status: "failed", prompt: process.env.AHT_TEST_SENSITIVE_SENTINEL },
  result: { status: "failed" },
});
