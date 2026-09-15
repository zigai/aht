import plugin from "./index.js";
import { createInterface } from "node:readline";
const lines = createInterface({input: process.stdin})[Symbol.asyncIterator]();
plugin.setup({}, {session: {sessionId: "session"}});
await lines.next();
for (let index = 0; index < 200; index++) plugin.hooks.beforeRun({snapshot: {runId: String(index)}});
const final = plugin.hooks.afterRun({snapshot: {}, result: {status: "failed"}});
await final;
await lines.next();
