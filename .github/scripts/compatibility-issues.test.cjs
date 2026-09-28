const assert = require("node:assert/strict");
const { test } = require("node:test");
const { syncIssues } = require("./compatibility-issues.cjs");

const context = { repo: { owner: "fixture", repo: "aht" } };
const core = { info() {} };
const bot = { login: "github-actions[bot]" };
const grok = { harness: "grok", version: "1.0.41", successful: "1.0.39", run_url: "https://example.invalid/run/1" };

function issuesFixture(open) {
  const calls = [];
  const issues = {
    listForRepo: "list",
    async create(params) { calls.push(["create", params]); return { data: { number: 99 } }; },
    async update(params) { calls.push(["update", params]); return { data: {} }; },
    async createComment(params) { calls.push(["comment", params]); return { data: {} }; },
  };
  const github = { rest: { issues }, async paginate(endpoint, params) {
    assert.equal(endpoint, "list");
    assert.equal(params.state, "open");
    assert.equal(params.creator, "github-actions[bot]");
    return open;
  } };
  return { args: { github, context, core }, calls };
}

async function openedBody(item) {
  const fixture = issuesFixture([]);
  await syncIssues({ ...fixture.args, report: { open: [item], resolved: [] } });
  return fixture.calls[0][1].body;
}

test("opens one issue for a regression without a tracked issue", async () => {
  const fixture = issuesFixture([]);
  await syncIssues({ ...fixture.args, report: { open: [grok], resolved: [] } });
  assert.equal(fixture.calls.length, 1);
  const [kind, params] = fixture.calls[0];
  assert.equal(kind, "create");
  assert.equal(params.title, "Compatibility regression: grok");
  assert.match(params.body, /\*\*grok 1\.0\.41\*\*/);
  assert.match(params.body, /Last successful release: 1\.0\.39/);
  assert.match(params.body, /^<!-- aht-compatibility:grok -->$/m);
});

test("leaves an unchanged tracked issue alone and updates a stale one", async () => {
  const current = { number: 5, title: "Compatibility regression: grok", body: await openedBody(grok), user: bot };
  const unchanged = issuesFixture([current]);
  await syncIssues({ ...unchanged.args, report: { open: [grok], resolved: [] } });
  assert.deepEqual(unchanged.calls, []);

  const stale = issuesFixture([{ ...current, body: await openedBody({ ...grok, version: "1.0.40" }) }]);
  await syncIssues({ ...stale.args, report: { open: [grok], resolved: [] } });
  assert.equal(stale.calls.length, 1);
  assert.equal(stale.calls[0][0], "update");
  assert.equal(stale.calls[0][1].issue_number, 5);
  assert.match(stale.calls[0][1].body, /grok 1\.0\.41/);
});

test("comments on and closes the tracked issue once the harness resolves", async (t) => {
  const cases = [
    { name: "passed", item: { harness: "grok", version: "1.0.42", run_url: "https://example.invalid/run/2", reason: "passed" }, comment: /grok 1\.0\.42 passed in https:\/\/example\.invalid\/run\/2/ },
    { name: "above maximum", item: { harness: "grok", version: "1.0.42", max_version: "1.0.41", reason: "above supported maximum" }, comment: /above the supported maximum 1\.0\.41/ },
  ];
  for (const item of cases) {
    await t.test(item.name, async () => {
      const fixture = issuesFixture([{ number: 5, title: "Compatibility regression: grok", body: await openedBody(grok), user: bot }]);
      await syncIssues({ ...fixture.args, report: { open: [], resolved: [item.item] } });
      assert.equal(fixture.calls.length, 2);
      assert.equal(fixture.calls[0][0], "comment");
      assert.equal(fixture.calls[0][1].issue_number, 5);
      assert.match(fixture.calls[0][1].body, item.comment);
      assert.deepEqual(fixture.calls[1], ["update", { ...context.repo, issue_number: 5, state: "closed", state_reason: "completed" }]);
    });
  }
});

test("ignores issues and pull requests the workflow does not own", async () => {
  const markerBody = await openedBody(grok);
  const fixture = issuesFixture([
    { number: 1, title: "Compatibility regression: grok", body: markerBody, user: { login: "someone" } },
    { number: 2, title: "Compatibility regression: grok", body: markerBody, user: bot, pull_request: {} },
    { number: 3, title: "Unrelated", body: "mentions grok", user: bot },
  ]);
  await syncIssues({ ...fixture.args, report: { open: [], resolved: [{ ...grok, reason: "passed" }] } });
  assert.deepEqual(fixture.calls, []);
});
