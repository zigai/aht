const fs = require("node:fs");
const capture = process.env.AHT_PROCESS_CAPTURE;
const first = !fs.existsSync(capture);
let overlap = false;
if (!first) {
  const previous = JSON.parse(fs.readFileSync(capture, "utf8").trim().split("\n").at(-1));
  try {
    process.kill(previous.pid, 0);
    overlap = true;
  } catch {}
}
if (first) process.on("SIGTERM", () => {});
fs.appendFileSync(
  capture,
  JSON.stringify({ pid: process.pid, args: process.argv.slice(2), overlap }) + "\n",
);
if (first) {
  setInterval(() => {}, 1000);
  // Self-clean even when exercising the old detached-child bug.
  setTimeout(() => process.exit(0), 10000);
}
