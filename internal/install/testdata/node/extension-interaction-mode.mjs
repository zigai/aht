import extension from "./extension.ts";
process.title = "pi";
const hooks = new Map();
extension({ on: (name, handler) => hooks.set(name, handler) });
const ctx = {
  mode: process.env.AHT_TEST_MODE,
  hasUI: true,
  cwd: "/tmp/project",
  sessionManager: {
    getSessionId: () => "mode-session",
    getSessionFile: () => "/tmp/mode.jsonl",
    getBranch: () => [],
  },
};
await hooks.get("session_start")({ type: "session_start" }, ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
