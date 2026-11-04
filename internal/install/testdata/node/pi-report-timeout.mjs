import assert from "node:assert/strict";
import extension from "./extension.ts";
// The runtime harness kills drivers after five seconds, before the real deadline.
const realSetTimeout = globalThis.setTimeout;
globalThis.setTimeout = (callback, delay, ...args) => realSetTimeout(callback, delay / 100, ...args);
const hooks = new Map();
const entries = [];
const notifications = [];
const statuses = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  appendEntry: (type, data) => entries.push({ type, data }),
});
const ui = {
  notify: (message) => notifications.push(message),
  setStatus: (key, text) => statuses.push({ key, text }),
};
hooks.get("session_start")({ type: "session_start" }, { hasUI: true, ui });
await hooks.get("session_shutdown")({ type: "session_shutdown" }, { hasUI: true, ui });
assert.equal(entries[0].type, "aht-reporting");
assert.match(entries[0].data.message, /timed out/);
assert.deepEqual(notifications, []);
assert.deepEqual(statuses.filter(({ text }) => text !== undefined), []);
