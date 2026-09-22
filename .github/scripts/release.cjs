// GitHub's native release and workflow APIs are injected by actions/github-script.
// Keep these decisions independently testable without publishing real releases.
const requiredChecks = ["verify-linux", "verify-darwin", "artifact-validation", "change-compatibility"];

function identity(context, sha) {
  if (!/^[a-f0-9]{40}$/.test(sha) || !/^refs\/tags\/v[^/]+$/.test(context.ref)) {
    throw new Error("A release requires a resolved commit and a version tag");
  }
  const tag = context.ref.slice("refs/tags/".length);
  const marker = `<!-- aht-release:${context.repo.owner}/${context.repo.repo}:${context.runId}:${sha} -->`;
  return { tag, marker };
}

async function requireCI({ github, context, core, sha }) {
  identity(context, sha);
  const runs = await github.paginate(github.rest.actions.listWorkflowRuns, {
    ...context.repo, workflow_id: "ci.yml", event: "push", branch: "master", head_sha: sha, per_page: 100,
  });
  const run = runs.filter((item) => item.head_sha === sha && item.event === "push" &&
    item.head_branch === "master" && item.path === ".github/workflows/ci.yml")
    .sort((a, b) => b.id - a.id)[0];
  if (!run || run.status !== "completed" || run.conclusion !== "success") {
    throw new Error(`CI for ${sha} must complete successfully before release (latest: ${run?.status ?? "missing"}/${run?.conclusion ?? "none"}). Rerun Release after CI succeeds.`);
  }
  const jobs = await github.paginate(github.rest.actions.listJobsForWorkflowRun, {
    ...context.repo, run_id: run.id, filter: "latest", per_page: 100,
  });
  for (const name of requiredChecks) {
    const matches = jobs.filter((job) => job.name === name);
    if (matches.length !== 1 || matches[0].conclusion !== "success") {
      throw new Error(`CI run ${run.id} did not successfully execute required check ${name}`);
    }
  }
  core.info(`Verified all required checks for ${sha}: ${run.html_url}`);
}

async function findRelease(github, context, tag) {
  try {
    return (await github.rest.repos.getReleaseByTag({ ...context.repo, tag })).data;
  } catch (error) {
    if (error.status === 404) return null;
    throw error;
  }
}

function owned(release, context, sha) {
  const { tag, marker } = identity(context, sha);
  return release.tag_name === tag && release.target_commitish === sha &&
    (release.body ?? "").split("\n").includes(marker);
}

async function prepareDraft({ github, context, core, sha, notes }) {
  const { tag, marker } = identity(context, sha);
  let release = await findRelease(github, context, tag);
  if (!release) {
    // The marker is part of creation, so cleanup can reconcile a lost response.
    release = (await github.rest.repos.createRelease({
      ...context.repo, tag_name: tag, target_commitish: sha, name: tag,
      draft: true, prerelease: tag.includes("-"), body: `${notes.trimEnd()}\n\n${marker}\n`,
    })).data;
  }
  if (!owned(release, context, sha)) {
    throw new Error(`Release ${tag} belongs to another run; refusing to replace it`);
  }
  core.setOutput("release-id", release.id);
  core.setOutput("published", !release.draft);
  core.info(`${release.draft ? "Prepared draft" : "Already published"} release ${release.id}`);
}

async function publishDraft({ github, context, core, sha, releaseID }) {
  const params = { ...context.repo, release_id: releaseID };
  const release = (await github.rest.repos.getRelease(params)).data;
  if (!owned(release, context, sha)) throw new Error("Refusing to publish a release owned by another run");
  if (!release.draft) return;
  try {
    await github.rest.repos.updateRelease({ ...params, draft: false });
  } catch (error) {
    const current = (await github.rest.repos.getRelease(params)).data;
    if (!owned(current, context, sha) || current.draft) throw error;
    core.info("Publication succeeded despite a lost API response");
  }
}

async function cleanupDraft({ github, context, core, sha }) {
  const { tag } = identity(context, sha);
  const release = await findRelease(github, context, tag);
  if (!release || !release.draft || !owned(release, context, sha)) {
    core.info("No draft owned by this run needs cleanup");
    return;
  }
  await github.rest.repos.deleteRelease({ ...context.repo, release_id: release.id });
  core.info(`Deleted owned draft ${release.id}`);
}

module.exports = { requireCI, prepareDraft, publishDraft, cleanupDraft };
