import plugin from "./plugin.ts";

const handlers = new Map();
const amp = {
  logger: { log() {} },
  on(name, handler) {
    handlers.set(name, handler);
  },
  system: { workspaceRoot: "/tmp/project" },
  helpers: { filePathFromURI: (u) => u },
};

plugin(amp);

const sessionStart = handlers.get("session.start");
const agentEnd = handlers.get("agent.end");

await sessionStart({ thread: { id: "T-test-thread" } }, { thread: { id: "T-test-thread" } });
await agentEnd(
  {
    thread: { id: "T-test-thread" },
    status: "error",
    message: process.env.AHT_TEST_SENSITIVE_SENTINEL,
  },
  { thread: { id: "T-test-thread" } },
);
