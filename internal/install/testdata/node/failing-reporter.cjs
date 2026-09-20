const fs = require("node:fs");
if (!fs.existsSync(process.env.AHT_REPORT_RECOVERED)) {
  process.stderr.write(
    "writing temp store /private/SECRET: no space left on device\n" + "SECRET".repeat(2000),
  );
  process.exitCode = 1;
}
