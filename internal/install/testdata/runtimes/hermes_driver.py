import __init__ as plugin
class Context:
    def __init__(self):
        self.hooks = {}
    def register_hook(self, name, callback):
        self.hooks[name] = callback
ctx = Context()
plugin.register(ctx)
ctx.hooks["on_session_start"](session_id="session")
input()
for index in range(200):
    ctx.hooks["pre_llm_call"](session_id="session", turn_id=str(index))
ctx.hooks["on_session_finalize"](session_id="session")
input()
