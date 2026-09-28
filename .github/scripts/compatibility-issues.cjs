// GitHub's native issue APIs are injected by actions/github-script. The report
// comes from `compatibility finish`: one open issue tracks each harness whose
// latest supported release fails, so known regressions stay visible without
// failing every scheduled run.
const creator = "github-actions[bot]";
const markerPattern = /^<!-- aht-compatibility:([a-z0-9-]+) -->$/m;

function marker(harness) {
  return `<!-- aht-compatibility:${harness} -->`;
}

function title(item) {
  return `Compatibility regression: ${item.harness}`;
}

function body(item) {
  return [
    `The scheduled release compatibility check fails for **${item.harness} ${item.version}**.`,
    "",
    `- Last successful release: ${item.successful || "none"} (change checks in CI pin this release)`,
    `- Supported maximum: ${item.max_version || "latest"}`,
    `- Failing run: ${item.run_url || "unknown"}`,
    "",
    "The check retries each newer release and closes this issue when one passes. If the failure is an",
    "upstream incompatibility, set `MaxVersion` in the adapter's distribution to the last successful release.",
    "",
    marker(item.harness),
    "",
  ].join("\n");
}

function resolution(item) {
  if (item.reason === "above supported maximum") {
    return `${item.harness} ${item.version} is above the supported maximum ${item.max_version}, so the adapter's \`MaxVersion\` now tracks this failure.`;
  }
  return `${item.harness} ${item.version} passed in ${item.run_url || "the latest check"}.`;
}

async function trackedIssues(github, context) {
  const issues = await github.paginate(github.rest.issues.listForRepo, {
    ...context.repo, state: "open", creator, per_page: 100,
  });
  const tracked = new Map();
  for (const issue of issues) {
    if (issue.pull_request || issue.user?.login !== creator) continue;
    const match = markerPattern.exec(issue.body ?? "");
    if (match && !tracked.has(match[1])) tracked.set(match[1], issue);
  }
  return tracked;
}

async function syncIssues({ github, context, core, report }) {
  const tracked = await trackedIssues(github, context);
  for (const item of report.open) {
    const existing = tracked.get(item.harness);
    const params = { ...context.repo, title: title(item), body: body(item) };
    if (!existing) {
      const created = (await github.rest.issues.create(params)).data;
      core.info(`Opened #${created.number} for ${item.harness} ${item.version}`);
    } else if (existing.body !== params.body || existing.title !== params.title) {
      await github.rest.issues.update({ ...params, issue_number: existing.number });
      core.info(`Updated #${existing.number} for ${item.harness} ${item.version}`);
    }
  }
  for (const item of report.resolved) {
    const existing = tracked.get(item.harness);
    if (!existing) continue;
    const issue = { ...context.repo, issue_number: existing.number };
    await github.rest.issues.createComment({ ...issue, body: resolution(item) });
    await github.rest.issues.update({ ...issue, state: "closed", state_reason: "completed" });
    core.info(`Closed #${existing.number} for ${item.harness} ${item.version}`);
  }
}

module.exports = { syncIssues };
