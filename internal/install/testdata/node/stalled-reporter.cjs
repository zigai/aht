const fs = require("node:fs");
process.on("SIGTERM", () => {});
fs.appendFileSync(
  process.env.AHT_PROCESS_CAPTURE,
  JSON.stringify({ pid: process.pid, args: process.argv.slice(2), overlap: false }) + "\n",
);
setInterval(() => {}, 1000);
// Exit on its own if the test never kills this reporter.
setTimeout(() => process.exit(0), 10000);
