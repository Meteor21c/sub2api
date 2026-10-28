import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { snapshotAuthorized } from "../authorization.mjs";
import { createSnapshotReader } from "../snapshot-cache.mjs";

const credential = "fixture-private-monitor-token";

test("snapshot bearer rejects missing, malformed, query and empty configuration", () => {
  for (const value of [undefined, "", "Bearer ", "Basic xyz", `Bearer ${credential} extra`, `Bearer wrong`, `Bearer ${"x".repeat(5000)}`]) {
    assert.equal(snapshotAuthorized(value, credential), false);
  }
  assert.equal(snapshotAuthorized(`Bearer ${credential}`, ""), false);
  assert.equal(snapshotAuthorized(`Bearer ${credential}`, credential), true);
  assert.equal(snapshotAuthorized(`bearer ${credential}`, credential), true);
});

async function launch(t, token) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([name]) => !/^(UPSTREAM_|SMTP_|ALERT_)/.test(name)));
  const child = spawn(process.execPath, [fileURLToPath(new URL("../server.mjs", import.meta.url))], {
    env: { ...env, HOST: "127.0.0.1", PORT: "0", MONITOR_SNAPSHOT_TOKEN: token, ALERT_CRON_TOKEN: "fixture-private-alert-token" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  t.after(async () => { if (child.exitCode === null) { child.kill("SIGTERM"); await once(child, "exit"); } });
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("monitor fixture startup timed out")), 5000);
    child.stdout.on("data", (chunk) => {
      const match = String(chunk).match(/METEOR_LITE_READY port=(\d+)/);
      if (match) { clearTimeout(timeout); resolve(`http://127.0.0.1:${match[1]}`); }
    });
    child.once("exit", () => { clearTimeout(timeout); reject(new Error("monitor fixture exited before readiness")); });
  });
}

test("HTTP monitor never returns financial data without service bearer; owner browser uses main login", async (t) => {
  const origin = await launch(t, credential);
  for (const path of ["/api/snapshot", "/api/newapi/snapshot", `/api/snapshot?token=${credential}`]) {
    const response = await fetch(origin + path);
    assert.equal(response.status, 401);
    assert.equal(response.headers.get("cache-control"), "no-store");
    const body = await response.text();
    assert.ok(!body.includes("balance"));
    assert.ok(!body.includes("baseUrl"));
    assert.ok(!body.includes(credential));
  }
  const wrong = await fetch(origin + "/api/snapshot", { headers: { Authorization: "Bearer wrong" } });
  assert.equal(wrong.status, 401);
  const response = await fetch(origin + "/api/snapshot", { headers: { Authorization: `Bearer ${credential}` } });
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("vary"), "Authorization");
  const snapshot = await response.json();
  assert.equal(snapshot.accounts.length, 9);
  assert.ok(snapshot.accounts.every((account) => account.status === "waiting"));
  const repeat = await fetch(origin + "/api/snapshot", { headers: { Authorization: `Bearer ${credential}` } });
  assert.equal((await repeat.json()).generatedAt, snapshot.generatedAt, "authenticated refreshes reuse the short cache");
  const cachedUnauthorized = await fetch(origin + "/api/snapshot");
  assert.equal(cachedUnauthorized.status, 401, "a warm private cache still requires auth");
  assert.ok(!(await cachedUnauthorized.text()).includes("accounts"));
  for (const method of ["GET", "HEAD"]) {
    const homepage = await fetch(origin + "/", { method, redirect: "manual" });
    assert.equal(homepage.status, 303);
    assert.equal(homepage.headers.get("location"), "https://api.meteor21c.fun/admin/business-monitor");
    assert.equal(homepage.headers.get("cache-control"), "no-store");
    assert.equal(homepage.headers.get("referrer-policy"), "no-referrer");
    assert.equal(await homepage.text(), "");
  }
  for (const path of ["/app.js", "/styles.css", "/index.html"]) {
    const retired = await fetch(origin + path);
    assert.equal(retired.status, 404);
    assert.equal(retired.headers.get("cache-control"), "no-store");
    assert.deepEqual(await retired.json(), { success: false, message: "not found" });
  }
  for (const path of ["/api/alerts/check", "/api/newapi/alerts/check"]) {
    assert.equal((await fetch(origin + path, { method: "POST" })).status, 401);
  }
  assert.equal((await fetch(origin + "/healthz")).status, 200);
});

test("snapshot authentication fails closed when operator forgot token", async (t) => {
  const origin = await launch(t, "");
  const response = await fetch(origin + "/api/snapshot");
  assert.equal(response.status, 503);
  assert.ok(!(await response.text()).includes("accounts"));
});

test("private snapshot coalesces concurrent calls, expires at15s, retries failed reads", async () => {
  let clock = 0;
  let calls = 0;
  let release;
  const blocked = new Promise((resolve) => { release = resolve; });
  const read = createSnapshotReader(async () => { calls += 1; await blocked; return { generatedAt: calls }; }, { now: () => clock, ttlMs: 15_000 });
  const first = read();
  const second = read();
  await Promise.resolve();
  assert.equal(calls, 1);
  release();
  assert.deepEqual(await first, await second);
  clock = 14_999;
  assert.equal((await read()).generatedAt, 1);
  clock = 15_000;
  assert.equal((await read()).generatedAt, 2);
  let attempts = 0;
  const retry = createSnapshotReader(async () => { if (++attempts === 1) throw new Error("fixture failure"); return { ok: true }; });
  await assert.rejects(retry());
  assert.deepEqual(await retry(), { ok: true });
});
