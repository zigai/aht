import plugin from "./extension.ts";
import { pluginHooks } from "./plugin-hooks.mjs";
const hooks = await pluginHooks(plugin, undefined);
await hooks.event({ event: { type: "session.created", sessionID: "missing-binary" } });
await hooks.dispose();
