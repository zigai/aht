import extension from "./extension.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  hasUI: true,
  cwd: "/tmp/project",
  sessionManager: {
    getSessionId: () => "omp-session",
    getSessionFile: () => "/tmp/omp.jsonl",
    getBranch: () => [],
  },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
hooks.get("agent_error")(
  { type: "agent_error", error: "fatal", prompt: process.env.AHT_TEST_SENSITIVE_SENTINEL },
  ctx,
);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
