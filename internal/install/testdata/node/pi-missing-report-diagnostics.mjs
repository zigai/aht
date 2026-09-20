import assert from "node:assert/strict";
import extension from "./extension.ts";
const hooks = new Map();
const notifications = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  appendEntry: () => {
    throw new Error("session disk is full too");
  },
});
await hooks.get("session_shutdown")(
  { type: "session_shutdown" },
  { hasUI: true, ui: { notify: (message) => notifications.push(message) } },
);
assert.equal(notifications.length, 1);
assert.match(notifications[0], /reporter executable is missing/);
