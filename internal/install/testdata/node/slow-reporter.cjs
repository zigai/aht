const fs = require("node:fs");
// A successful report whose disk sync takes longer than one second.
setTimeout(() => fs.writeFileSync(process.env.AHT_REPORT_COMPLETED, "written"), 1300);
