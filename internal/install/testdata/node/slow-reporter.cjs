const fs = require("node:fs");
// Model a successful disk sync that exceeds the old one-second deadline.
setTimeout(() => fs.writeFileSync(process.env.AHT_REPORT_COMPLETED, "written"), 1300);
