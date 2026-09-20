import plugin from "./index.js";
const hooks = new Map();
plugin.register({ on: (name, callback) => hooks.set(name, callback) });
await hooks.get("agent_end")(
  { success: false, error: "native error", prompt: process.env.AHT_TEST_SENSITIVE_SENTINEL },
  { sessionId: "openclaw-session", workspaceDir: "/tmp/project" },
);
