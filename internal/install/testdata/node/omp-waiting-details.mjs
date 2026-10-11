import extension from "./extension.ts";
import { readFileSync } from "node:fs";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = { mode: "rpc", sessionManager: { getSessionId: () => "detail-session", getBranch: () => [] } };
function count() { try { return readFileSync(process.env.AHT_CAPTURE, "utf8").split("---").filter((part) => part.trim()).length; } catch { return 0; } }
async function emit(type, fields, expected) {
  await hooks.get(type)({ type, ...fields }, ctx);
  const end = Date.now() + 2500;
  while (count() < expected && Date.now() < end) await new Promise((resolve) => setTimeout(resolve, 5));
  if (count() < expected) throw new Error(`missing state ${type}`);
}
await emit("session_start", {}, 1);
await emit("agent_start", {}, 2);
await emit("tool_approval_requested", { toolCallId: "a", toolName: "bash" }, 3);
await emit("tool_approval_requested", { toolCallId: "a", toolName: "bash" }, 3);
await emit("tool_execution_start", { toolCallId: "q", toolName: "ask" }, 4);
await emit("tool_approval_resolved", { toolCallId: "unrelated", toolName: "bash", approved: true }, 4);
await emit("tool_approval_resolved", { toolCallId: "a", toolName: "bash", approved: true }, 5);
await emit("tool_execution_end", { toolCallId: "q", toolName: "ask" }, 6);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
