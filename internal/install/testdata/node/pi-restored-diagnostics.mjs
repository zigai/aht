import assert from "node:assert/strict";
import extension from "./extension.ts";
const hooks = new Map();
const commands = new Map();
const notifications = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  registerCommand: (name, options) => commands.set(name, options.handler),
});
const ctx = {
  hasUI: true,
  ui: { notify: (message) => notifications.push(message) },
  sessionManager: {
    getEntries: () => [
      {
        type: "custom",
        customType: "aht-reporting",
        data: { reason: "state write failed: disk is full" },
      },
      { type: "custom", customType: "unrelated", data: { reason: "ignore this" } },
      { type: "custom", customType: "aht-reporting", data: null },
    ],
  },
};
hooks.get("session_start")({ type: "session_start" }, ctx);
await hooks.get("session_shutdown")({ type: "session_shutdown" }, ctx);
await commands.get("aht-status")("", ctx);
assert.match(notifications.at(-1), /no current failure/);
assert.match(notifications.at(-1), /Last failure: state write failed: disk is full/);
