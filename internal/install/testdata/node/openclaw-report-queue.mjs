import plugin from "./index.js";
import { createInterface } from "node:readline";
const lines = createInterface({ input: process.stdin })[Symbol.asyncIterator]();
const hooks = new Map();
plugin.register({ on: (name, callback) => hooks.set(name, callback) });
const ctx = { sessionId: "session" };
hooks.get("session_start")({}, ctx);
if (process.env.AHT_TEST_SKIP_HANDSHAKE !== "1") await lines.next();
for (let index = 0; index < 200; index++)
  hooks.get("before_agent_run")({}, { ...ctx, runId: String(index) });
if (process.env.AHT_TEST_GATEWAY_STOP === "1") {
  hooks.get("session_end")({}, ctx);
  await hooks.get("gateway_stop")();
} else {
  await hooks.get("session_end")({}, ctx);
}
if (process.env.AHT_TEST_SKIP_HANDSHAKE !== "1") await lines.next();
