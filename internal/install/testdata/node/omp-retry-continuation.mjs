import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  mode: "rpc",
  sessionManager: { getSessionId: () => "retry-session", getBranch: () => [] },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
hooks.get("agent_start")({ type: "agent_start" }, ctx);
hooks.get("agent_end")(JSON.parse(process.env.AHT_TEST_EVENT), ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
