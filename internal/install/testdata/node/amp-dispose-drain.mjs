import assert from "node:assert/strict";
import fs from "node:fs";
import plugin from "./plugin.ts";

const handlers = new Map();
const disposers = [];
const amp = {
  on: (name, handler) => handlers.set(name, handler),
  onDispose: (callback) => disposers.push(callback),
  threads: { get: () => ({ title: { get: async () => "" } }) },
  system: { workspaceRoot: "/tmp/project" },
  helpers: { filePathFromURI: (uri) => uri },
};
plugin(amp);
const thread = { id: "T-dispose-thread" };
const ctx = { thread };
handlers.get("session.start")({ thread }, ctx);
handlers.get("agent.start")({ thread }, ctx);
handlers.get("agent.end")({ thread, status: "done" }, ctx);
// Amp waits for the promises dispose callbacks return, then terminates the plugin process.
await Promise.all(disposers.map((dispose) => dispose()));
const reported = fs.readFileSync(process.env.AHT_REPORT_LOG, "utf8").trim().split("\n").map((line) => JSON.parse(line));
const reportedEvents = reported.map((args) => args[args.indexOf("--event") + 1]);
assert.deepEqual(reportedEvents, ["session.start", "agent.start", "agent.end", "session.end"]);
process.exit(0);
