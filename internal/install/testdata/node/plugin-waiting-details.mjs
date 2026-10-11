import plugin from "./plugin.ts";
const hooks = await plugin.server({ directory: "/work" });
async function emit(type, sessionID, fields = {}) { await hooks.event({ event: { type, properties: { sessionID, ...fields } } }); }
await emit("session.created", "a");
await emit("question.unrecognized", "a");
await emit("permission.asked", "a", { id: "one" });
await emit("permission.asked", "a", { id: "two" });
await emit("permission.replied", "a", { requestID: "unrelated" });
await emit("permission.replied", "a", { requestID: "one" });
await emit("session.status", "a", { status: { type: "busy" } });
await emit("session.created", "b");
await emit("session.status", "b", { status: { type: "retry" } });
if (process.env.AHT_TEST_QUESTIONS === "1") {
  await emit("question.asked", "a", { id: "q" });
  await emit("permission.replied", "a", { requestID: "two" });
  await emit("question.rejected", "a", { requestID: "q" });
} else await emit("permission.replied", "a", { requestID: "two" });
await emit("session.error", "a");
await emit("session.status", "a", { status: { type: "retry" } });
await hooks.dispose?.();
