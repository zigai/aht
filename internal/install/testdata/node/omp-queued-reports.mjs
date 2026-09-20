import { createInterface } from "node:readline";
import extension from "./extension.ts";
const lines = createInterface({ input: process.stdin })[Symbol.asyncIterator]();
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
let id = "original";
const ctx = {
  mode: "tui",
  cwd: "/work/original",
  sessionManager: {
    getSessionId: () => id,
    getSessionFile: () => "/sessions/" + id + ".jsonl",
    getBranch: () => [],
  },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
await lines.next();
hooks.get("agent_start")({ type: "agent_start" }, ctx);
for (let index = 0; index < 200; index += 1) {
  hooks.get("tool_approval_requested")(
    { type: "tool_approval_requested", reason: "approval-" + index },
    ctx,
  );
}
const event = {
  type: "agent_end",
  reason: "interrupted",
  toolName: "bash",
  approvalMode: "ask",
  approved: false,
};
hooks.get("agent_end")(event, ctx);
event.reason = "changed-after-enqueue";
id = "resumed";
ctx.cwd = "/work/resumed";
ctx.mode = "rpc";
hooks.get("session_switch")({ type: "session_switch", reason: "resume" }, ctx);
const shutdown = hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
id = "changed-after-shutdown";
ctx.cwd = "/work/changed";
ctx.mode = "print";
await shutdown;
await lines.next();
