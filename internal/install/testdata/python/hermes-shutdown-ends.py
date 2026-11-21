import __init__ as plugin


class Context:
    def __init__(self):
        self.hooks = {}

    def register_hook(self, name, callback):
        self.hooks[name] = callback


ctx = Context()
plugin.register(ctx)
# One gateway process hosts both sessions.
for session_id in ("first", "second"):
    ctx.hooks["on_session_start"](session_id=session_id)
for index in range(8):
    ctx.hooks["pre_llm_call"](session_id="second", turn_id=str(index))
ctx.hooks["on_session_finalize"](session_id="first")
ctx.hooks["pre_llm_call"](session_id="second", turn_id="last")
# Hermes CLI cleanup can finalize after the reporter's own atexit handler ran.
plugin.reporter.close()
ctx.hooks["on_session_finalize"](session_id="second")
