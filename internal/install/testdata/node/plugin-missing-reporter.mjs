import plugin from "./extension.ts";
const hooks = await plugin.server({});
await hooks.event({ event: { type: "session.created", sessionID: "missing-binary" } });
