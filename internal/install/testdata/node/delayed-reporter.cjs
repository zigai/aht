const fs = require("node:fs");
setTimeout(() => fs.appendFileSync(process.env.AHT_REPORT_LOG, JSON.stringify(process.argv.slice(2)) + "\n"), 100);
