import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  mode: "tui",
  sessionManager: { getSessionId: () => "approval-session", getBranch: () => [] },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
hooks.get("tool_approval_requested")(
  {
    type: "tool_approval_requested",
    reason: "Run command?",
    toolName: "bash",
    approvalMode: "ask",
  },
  ctx,
);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
