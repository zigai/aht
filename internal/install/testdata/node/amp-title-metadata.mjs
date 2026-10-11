import plugin from "./plugin.ts";
import { readFileSync } from "node:fs";
const handlers = new Map();
let stateCallback;
let titleCallback;
const thread = {
  id: "T-detail",
  state: { subscribe: (callback) => { stateCallback = callback; } },
  title: { subscribe: (callback) => { titleCallback = callback; }, get: async () => "title" },
};
plugin({ on: (name, handler) => handlers.set(name, handler), threads: { get: () => thread }, system: { workspaceRoot: "/work" } });
await handlers.get("session.start")({ thread: { id: "T-detail" } }, { thread });
stateCallback("awaiting-approval");
const end = Date.now() + 2500;
function count() { try { return readFileSync(process.env.AHT_CAPTURE, "utf8").split("---").filter((part) => part.trim()).length; } catch { return 0; } }
while (count() < 2 && Date.now() < end) await new Promise((resolve) => setTimeout(resolve, 5));
if (count() < 2) throw new Error("missing waiting state");
await titleCallback();
