// Kilo plugins expose server() hooks. OpenCode plugins read bus events from
// the subscription setup() opens, so each event() resolves once the plugin
// asks for the next event, after it has handled this one.
export async function pluginHooks(plugin, directory) {
  if (typeof plugin.server === "function") return plugin.server({ directory, worktree: directory });

  const queue = [];
  const handled = [];
  let wake;
  async function* events(signal) {
    signal?.addEventListener("abort", () => wake?.());
    while (!signal?.aborted) {
      if (queue.length === 0) await new Promise((resolve) => { wake = resolve; });
      const item = queue.shift();
      if (item === undefined) continue;
      yield item.event;
      item.done();
    }
  }
  const cleanup = await plugin.setup({
    location: { directory, project: { canonical: directory } },
    event: { subscribe: ({ signal } = {}) => events(signal) },
  });
  return {
    event({ event }) {
      const done = new Promise((resolve) => queue.push({ event, done: resolve }));
      handled.push(done);
      wake?.();
      return done;
    },
    async dispose() {
      await Promise.all(handled);
      await cleanup();
    },
  };
}
