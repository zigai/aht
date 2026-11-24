import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  mode: "rpc",
  sessionManager: { getSessionId: () => "retry-session", getBranch: () => [] },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
hooks.get("agent_start")({ type: "agent_start" }, ctx);
for (const event of JSON.parse(process.env.AHT_TEST_EVENTS)) {
  hooks.get(event.type)(event, ctx);
}
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
