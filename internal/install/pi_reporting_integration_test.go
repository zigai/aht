//go:build integration

package install

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestPiReportingAllowsSlowSuccessfulWrite(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "reporter")
	completed := filepath.Join(dir, "completed")
	t.Setenv("AHT_REPORT_COMPLETED", completed)
	writeTestFile(t, binary, "#!"+requireRuntimeTool(t, "node")+"\n"+`
const fs = require("node:fs");
// Model a successful disk sync that exceeds the old one-second deadline.
setTimeout(() => fs.writeFileSync(process.env.AHT_REPORT_COMPLETED, "written"), 1300);
`, 0o700)
	runNodeRuntime(t, "extension.ts", piReportingArtifact(t, binary), `
import assert from "node:assert/strict";
import fs from "node:fs";
import extension from "./extension.ts";
const hooks = new Map();
const failures = [];
extension({on: (name, callback) => hooks.set(name, callback), appendEntry: (...args) => failures.push(args)});
await hooks.get("session_shutdown")({type: "session_shutdown"}, {hasUI: false});
assert.equal(fs.readFileSync(process.env.AHT_REPORT_COMPLETED, "utf8"), "written");
assert.deepEqual(failures, []);
`, nil)
}

func TestPiReportingRetainsSafeNativeDiagnosticsAndRecovers(t *testing.T) {
	for _, hasUI := range []string{"true", "false"} {
		t.Run("hasUI="+hasUI, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "reporter")
			t.Setenv("AHT_REPORT_RECOVERED", filepath.Join(dir, "recovered"))
			t.Setenv("AHT_TEST_HAS_UI", hasUI)
			writeTestFile(t, binary, "#!"+requireRuntimeTool(t, "node")+"\n"+`
const fs = require("node:fs");
if (!fs.existsSync(process.env.AHT_REPORT_RECOVERED)) {
  process.stderr.write("writing temp store /private/SECRET: no space left on device\n" + "SECRET".repeat(2000));
  process.exitCode = 1;
}
`, 0o700)
			runNodeRuntime(t, "extension.ts", piReportingArtifact(t, binary), `
import assert from "node:assert/strict";
import fs from "node:fs";
import extension from "./extension.ts";
const hooks = new Map();
const commands = new Map();
const entries = [];
const notifications = [];
const statuses = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  registerCommand: (name, options) => commands.set(name, options.handler),
  appendEntry: (type, data) => entries.push({type, data}),
});
const hasUI = process.env.AHT_TEST_HAS_UI === "true";
const ui = {notify: (message) => notifications.push(message), setStatus: (key, text) => statuses.push({key, text})};
const ctx = {hasUI, ui};
hooks.get("session_start")({type: "session_start"}, ctx);
await hooks.get("session_shutdown")({type: "session_shutdown"}, ctx);
assert.equal(entries.length, 1, "repeated failures should not flood session history");
assert.equal(entries[0].type, "aht-reporting");
assert.match(entries[0].data.message, /disk is full/);
assert.doesNotMatch(JSON.stringify({entries, notifications, statuses}), /SECRET|private/);
assert.equal(notifications.length, hasUI ? 1 : 0);
assert.equal(statuses.length, hasUI ? 1 : 0);
await commands.get("aht-status")("", {hasUI: true, ui});
assert.match(notifications.at(-1), /degraded.*\nLast failure: state write failed: disk is full/s);
fs.writeFileSync(process.env.AHT_REPORT_RECOVERED, "ready");
await hooks.get("session_shutdown")({type: "session_shutdown"}, ctx);
if (hasUI) assert.deepEqual(statuses.at(-1), {key: "aht", text: undefined});
await commands.get("aht-status")("", {hasUI: true, ui});
assert.match(notifications.at(-1), /no current failure/);
assert.match(notifications.at(-1), /Last failure: state write failed: disk is full/);
`, nil)
		})
	}
}

func TestPiReportingMissingBinaryAndUnwritableSessionAreNonfatal(t *testing.T) {
	runNodeRuntime(t, "extension.ts", piReportingArtifact(t, filepath.Join(t.TempDir(), "missing")), `
import assert from "node:assert/strict";
import extension from "./extension.ts";
const hooks = new Map();
const notifications = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  appendEntry: () => { throw new Error("session disk is full too"); },
});
await hooks.get("session_shutdown")({type: "session_shutdown"}, {hasUI: true, ui: {notify: (message) => notifications.push(message)}});
assert.equal(notifications.length, 1);
assert.match(notifications[0], /reporter executable is missing/);
`, nil)
}

func TestPiReportingRestoresLastFailureAfterReload(t *testing.T) {
	capture := captureBinary(t)
	t.Setenv("AHT_CAPTURE", capture.path)
	runNodeRuntime(t, "extension.ts", piReportingArtifact(t, capture.command), `
import assert from "node:assert/strict";
import extension from "./extension.ts";
const hooks = new Map();
const commands = new Map();
const notifications = [];
extension({
  on: (name, callback) => hooks.set(name, callback),
  registerCommand: (name, options) => commands.set(name, options.handler),
});
const ctx = {hasUI: true, ui: {notify: (message) => notifications.push(message)}, sessionManager: {
  getEntries: () => [
    {type: "custom", customType: "aht-reporting", data: {reason: "state write failed: disk is full"}},
    {type: "custom", customType: "unrelated", data: {reason: "ignore this"}},
    {type: "custom", customType: "aht-reporting", data: null},
  ],
}};
hooks.get("session_start")({type: "session_start"}, ctx);
await hooks.get("session_shutdown")({type: "session_shutdown"}, ctx);
await commands.get("aht-status")("", ctx);
assert.match(notifications.at(-1), /no current failure/);
assert.match(notifications.at(-1), /Last failure: state write failed: disk is full/);
`, nil)
}

func piReportingArtifact(t *testing.T, binary string) string {
	t.Helper()
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: binary}) {
		if artifact.harness == registry.HarnessPi && strings.HasSuffix(artifact.path, "aht-state.ts") {
			return artifact.content
		}
	}
	t.Fatal("missing generated Pi extension")
	return ""
}
