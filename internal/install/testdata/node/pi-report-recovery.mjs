import assert from "node:assert/strict";
import fs from "node:fs";
import extension from "./extension.ts";
const hooks = new Map();
const commands = new Map();
const entries = [];
const notifications = [];
const statuses = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  registerCommand: (name, options) => commands.set(name, options.handler),
  appendEntry: (type, data) => entries.push({ type, data }),
});
const hasUI = process.env.AHT_TEST_HAS_UI === "true";
const ui = {
  notify: (message) => notifications.push(message),
  setStatus: (key, text) => statuses.push({ key, text }),
};
const ctx = { hasUI, ui };
hooks.get("session_start")({ type: "session_start" }, ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
assert.equal(entries.length, 1, "repeated failures should not flood session history");
assert.equal(entries[0].type, "aht-reporting");
assert.match(entries[0].data.message, /disk is full/);
assert.doesNotMatch(JSON.stringify({ entries, notifications, statuses }), /SECRET|private/);
assert.equal(notifications.length, hasUI ? 1 : 0);
assert.equal(statuses.length, hasUI ? 1 : 0);
await commands.get("aht-status")("", { hasUI: true, ui });
assert.match(notifications.at(-1), /degraded.*\nLast failure: state write failed: disk is full/s);
fs.writeFileSync(process.env.AHT_REPORT_RECOVERED, "ready");
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
if (hasUI) assert.deepEqual(statuses.at(-1), { key: "aht", text: undefined });
await commands.get("aht-status")("", { hasUI: true, ui });
assert.match(notifications.at(-1), /no current failure/);
assert.match(notifications.at(-1), /Last failure: state write failed: disk is full/);
