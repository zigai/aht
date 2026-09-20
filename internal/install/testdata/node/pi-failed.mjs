import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  cwd: "/tmp/project",
  sessionManager: { getSessionId: () => "pi-session", getSessionFile: () => "/tmp/pi.jsonl" },
};
hooks.get("agent_end")(
  { type: "agent_end", status: "failed", prompt: process.env.AHT_TEST_SENSITIVE_SENTINEL },
  ctx,
);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
