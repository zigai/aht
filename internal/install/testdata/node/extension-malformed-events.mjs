import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  hasUI: true,
  sessionManager: { getBranch: () => [], getSessionId: () => "boundary-session" },
};
hooks.get("session_start")(null, ctx);
hooks.get("agent_start")({ type: ["not-a-string"] }, ctx);
hooks.get("agent_end")(
  {
    messages: [null, 7, { role: "assistant", stopReason: "aborted" }],
    error: { secret: "DO_NOT_REPORT" },
  },
  ctx,
);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
