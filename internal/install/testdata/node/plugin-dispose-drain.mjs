import assert from "node:assert/strict";
import fs from "node:fs";
import plugin from "./plugin.ts";
const runtime = await plugin.server({ directory: "/tmp/project", worktree: "/tmp/project" });
assert.equal(typeof runtime.dispose, "function");
const sessionID = "drain-session";
// The host publishes events without awaiting hooks and exits after dispose.
const events = [
  { type: "session.created", properties: { info: { id: sessionID } } },
  { type: "session.status", properties: { sessionID, status: { type: "busy" } } },
  { type: "permission.asked", properties: { id: "request", sessionID } },
  { type: "permission.replied", properties: { requestID: "request", sessionID } },
  { type: "session.status", properties: { sessionID, status: { type: "idle" } } },
  { type: "session.idle", properties: { sessionID } },
];
for (const event of events) void runtime.event({ event });
await runtime.dispose();
const reported = fs.readFileSync(process.env.AHT_REPORT_LOG, "utf8").trim().split("\n").map((line) => JSON.parse(line));
const reportedEvents = reported.map((args) => args[args.indexOf("--event") + 1]);
assert.deepEqual(reportedEvents, events.map((event) => event.type));
