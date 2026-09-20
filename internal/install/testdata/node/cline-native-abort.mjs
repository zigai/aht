import plugin from "./index.js";
await plugin.setup(
  {},
  { session: { sessionId: "cline-session" }, workspaceInfo: { rootPath: "/tmp/project" } },
);
await plugin.hooks.afterRun({ snapshot: { status: "running" }, result: { status: "aborted" } });
