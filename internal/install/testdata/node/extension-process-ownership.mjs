import { createInterface } from "node:readline";
const lines = createInterface({ input: process.stdin })[Symbol.asyncIterator]();
import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, handler) => hooks.set(name, handler) });
const ctx = {
  hasUI: true,
  cwd: "/project",
  sessionManager: {
    getSessionId: () => "owned-session",
    getSessionFile: () => "/project/session.jsonl",
    getBranch: () => [],
  },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
await lines.next();
for (let index = 0; index < 100; index++) {
  hooks.get("session_start")({ type: "session_start" }, ctx);
}
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
await lines.next();
process.stdin.destroy();
