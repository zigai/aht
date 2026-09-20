import assert from "node:assert/strict";
import fs from "node:fs";
import extension from "./extension.ts";
const hooks = new Map();
const failures = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  appendEntry: (...args) => failures.push(args),
});
await hooks.get("session_shutdown")({ type: "session_shutdown" }, { hasUI: false });
assert.equal(fs.readFileSync(process.env.AHT_REPORT_COMPLETED, "utf8"), "written");
assert.deepEqual(failures, []);
