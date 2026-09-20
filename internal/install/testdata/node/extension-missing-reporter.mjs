import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, handler) => hooks.set(name, handler) });
const ctx = {
  hasUI: true,
  sessionManager: { getBranch: () => [], getSessionId: () => "missing-binary" },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
