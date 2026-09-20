import extension from "./pi_absent.ts";
const hooks = new Map();
extension({ on: (name, callback) => hooks.set(name, callback) });
const ctx = {
  cwd: "/tmp/project",
  sessionManager: { getSessionId: () => "pi-session", getSessionFile: () => "/tmp/pi.jsonl" },
};
hooks.get("agent_end")({ type: "agent_end", status: "failed" }, ctx);
await new Promise((resolve) => setTimeout(resolve, 50));
