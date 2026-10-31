import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  mode: "rpc",
  sessionManager: { getSessionId: () => "continuation-session" },
};
const done = { type: "agent_end", messages: [{ role: "assistant", stopReason: "stop" }] };
hooks.get("session_start")({ type: "session_start" }, ctx);
hooks.get("agent_start")({ type: "agent_start" }, ctx);
hooks.get("agent_end")(done, ctx);
hooks.get("agent_start")({ type: "agent_start" }, ctx);
hooks.get("agent_end")(done, ctx);
hooks.get("agent_settled")({ type: "agent_settled", aborted: false }, ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
