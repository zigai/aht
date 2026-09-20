import importlib.util
import os
import sys

spec = importlib.util.spec_from_file_location("aht_state", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class Context:
    def __init__(self):
        self.hooks = {}

    def register_hook(self, name, callback):
        self.hooks[name] = callback


ctx = Context()
module.register(ctx)
ctx.hooks["on_session_end"](
    session_id="hermes-session",
    completed=False,
    prompt=os.environ["AHT_TEST_SENSITIVE_SENTINEL"],
)
