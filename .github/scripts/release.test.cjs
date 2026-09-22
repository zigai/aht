const assert = require("node:assert/strict");
const { test } = require("node:test");
const { requireCI, prepareDraft, publishDraft, cleanupDraft } = require("./release.cjs");

const sha = "a".repeat(40);
const context = { repo: { owner: "fixture", repo: "aht" }, ref: "refs/tags/v1.2.3", runId: 42 };
const core = { info() {}, setOutput() {} };
const successfulRun = {
  id: 10, head_sha: sha, event: "push", head_branch: "master", path: ".github/workflows/ci.yml",
  status: "completed", conclusion: "success", html_url: "https://example.invalid/run/10",
};
const successfulJobs = ["verify-linux", "verify-darwin", "artifact-validation", "change-compatibility"]
  .map((name) => ({ name, conclusion: "success" }));

test("release requires the newest exact-commit CI and every gate", async (t) => {
  const cases = [
    { name: "successful exact revision", runs: [successfulRun], jobs: successfulJobs, pass: true },
    { name: "missing run", runs: [], jobs: successfulJobs },
    { name: "other revision", runs: [{ ...successfulRun, head_sha: "b".repeat(40) }], jobs: successfulJobs },
    { name: "other workflow", runs: [{ ...successfulRun, path: ".github/workflows/other.yml" }], jobs: successfulJobs },
    { name: "PR result", runs: [{ ...successfulRun, event: "pull_request" }], jobs: successfulJobs },
    { name: "running", runs: [{ ...successfulRun, status: "in_progress", conclusion: null }], jobs: successfulJobs },
    { name: "cancelled latest run", runs: [successfulRun, { ...successfulRun, id: 11, conclusion: "cancelled" }], jobs: successfulJobs },
    { name: "missing compatibility gate", runs: [successfulRun], jobs: successfulJobs.slice(0, 3) },
    { name: "skipped compatibility gate", runs: [successfulRun], jobs: successfulJobs.map((job) => job.name === "change-compatibility" ? { ...job, conclusion: "skipped" } : job) },
  ];
  for (const item of cases) {
    await t.test(item.name, async () => {
      const actions = { listWorkflowRuns: "runs", listJobsForWorkflowRun: "jobs" };
      const github = { rest: { actions }, async paginate(endpoint, params) {
        if (endpoint === "runs") {
          assert.equal(params.head_sha, sha);
          assert.equal(params.workflow_id, "ci.yml");
          return item.runs;
        }
        assert.equal(params.run_id, 10);
        return item.jobs;
      } };
      const result = requireCI({ github, context, core, sha });
      if (item.pass) await result;
      else await assert.rejects(result);
    });
  }
});

function releaseFixture() {
  let release = null;
  const deleted = [];
  const outputs = {};
  const repos = {
    async getReleaseByTag() {
      if (!release) throw Object.assign(new Error("missing"), { status: 404 });
      return { data: release };
    },
    async getRelease() { return { data: release }; },
    async createRelease(params) { release = { ...params, id: 7 }; return { data: release }; },
    async updateRelease(params) { release = { ...release, ...params }; return { data: release }; },
    async deleteRelease(params) { deleted.push(params.release_id); release = null; },
  };
  return {
    args: { github: { rest: { repos } }, context, sha, core: { info() {}, setOutput(name, value) { outputs[name] = value; } }, notes: "## Features\n\nA change" },
    repos, deleted, outputs,
    get release() { return release; },
    set release(value) { release = value; },
  };
}

test("draft retries reuse ownership, preserve notes, and clean partial uploads", async () => {
  const fixture = releaseFixture();
  await prepareDraft(fixture.args);
  assert.equal(fixture.outputs["release-id"], 7);
  assert.equal(fixture.outputs.published, false);
  assert.match(fixture.release.body, /## Features/);
  fixture.repos.createRelease = async () => { throw new Error("must reuse draft"); };
  await prepareDraft(fixture.args);
  await cleanupDraft(fixture.args);
  assert.deepEqual(fixture.deleted, [7]);
});

test("cleanup reconciles a creation whose response was lost", async () => {
  const fixture = releaseFixture();
  const create = fixture.repos.createRelease;
  fixture.repos.createRelease = async (params) => { await create(params); throw new Error("connection reset"); };
  await assert.rejects(prepareDraft(fixture.args), /connection reset/);
  assert.equal(fixture.outputs["release-id"], undefined);
  await cleanupDraft(fixture.args);
  assert.deepEqual(fixture.deleted, [7]);
});

test("foreign and published releases cannot be deleted", async () => {
  for (const change of [{ draft: false }, { target_commitish: "b".repeat(40) }, { body: "another run" }]) {
    const fixture = releaseFixture();
    await prepareDraft(fixture.args);
    fixture.release = { ...fixture.release, ...change };
    await cleanupDraft(fixture.args);
    assert.deepEqual(fixture.deleted, []);
    if (change.draft !== false) await assert.rejects(prepareDraft(fixture.args), /another run/);
  }
});

test("lost publication response and reruns preserve the published release", async () => {
  const fixture = releaseFixture();
  await prepareDraft(fixture.args);
  const publish = fixture.repos.updateRelease;
  fixture.repos.updateRelease = async (params) => { await publish(params); throw new Error("response lost"); };
  await publishDraft({ ...fixture.args, releaseID: 7 });
  await cleanupDraft(fixture.args);
  await prepareDraft(fixture.args);
  assert.equal(fixture.outputs.published, true);
  assert.deepEqual(fixture.deleted, []);
});

test("publication failure remains a failure and its draft is removable", async () => {
  const fixture = releaseFixture();
  await prepareDraft(fixture.args);
  fixture.repos.updateRelease = async () => { throw new Error("unavailable"); };
  await assert.rejects(publishDraft({ ...fixture.args, releaseID: 7 }), /unavailable/);
  await cleanupDraft(fixture.args);
  assert.deepEqual(fixture.deleted, [7]);
});

test("an unauthorized lookup is not treated as an absent draft", async () => {
  const fixture = releaseFixture();
  fixture.repos.getReleaseByTag = async () => { throw Object.assign(new Error("forbidden"), { status: 403 }); };
  await assert.rejects(cleanupDraft(fixture.args), /forbidden/);
  await assert.rejects(prepareDraft(fixture.args), /forbidden/);
  assert.deepEqual(fixture.deleted, []);
});
