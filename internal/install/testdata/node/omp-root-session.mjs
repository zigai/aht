import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  hasUI: process.env.AHT_TEST_HAS_UI === "true",
  mode: process.env.AHT_TEST_MODE,
  cwd: "/tmp/project",
  sessionManager: {
    getSessionId: () => "omp-session",
    getSessionFile: () => "/tmp/omp.jsonl",
    getBranch: () => (process.env.AHT_TEST_SUBAGENT === "true" ? [{ type: "session_init" }] : []),
  },
};
await hooks.get("session_start")({ type: "session_start" }, ctx);
await hooks.get("agent_error")({ type: "agent_error", error: "fatal" }, ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
