import { createInterface } from "node:readline";
const lines = createInterface({ input: process.stdin })[Symbol.asyncIterator]();
import plugin from "./extension.ts";
const hooks = await plugin.server({ directory: "/project", worktree: "/project" });
const sends = [hooks.event({ event: { type: "session.created", sessionID: "owned-session" } })];
await lines.next();
for (let index = 0; index < 100; index++) {
  sends.push(
    hooks.event({
      event: {
        type: "session.status",
        properties: { sessionID: "owned-session", status: { type: "busy" } },
      },
    }),
  );
}
sends.push(hooks.event({ event: { type: "session.deleted", sessionID: "owned-session" } }));
await Promise.all(sends);
await lines.next();
process.stdin.destroy();
