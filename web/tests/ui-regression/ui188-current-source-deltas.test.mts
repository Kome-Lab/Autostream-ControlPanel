import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { historicalR4DeleteCopySource } from "./ui188-r4-delete-delta.mts";
import { historicalR5ReadinessSource } from "./ui188-r5-readiness-delta.mts";
import { assertUI188ContractRegistration, assertUI188CurrentContract, historicalUI188Source, inverseUI188CurrentSource, ui188CurrentContract } from "./ui188-current-source-deltas.mts";

const root = fileURLToPath(new URL("../../..", import.meta.url));
const object = (commit: string, path: string) => execFileSync("git", ["show", commit + ":" + path], { cwd: root, maxBuffer: 32 * 1024 * 1024 });
const raw = (path: string) => readFileSync(resolve(root, path));

test("UI188-CONTRACT-001: every finite path binds immutable historical source and actual current bytes to existing behavior tests", () => {
  for (const row of ui188CurrentContract.records) {
    const before = object(row.beforeCommit, row.path), current = historicalR4DeleteCopySource(row.path, historicalR5ReadinessSource(row.path, raw(row.path), object), object);
    assert.deepEqual(current, object(row.afterCommit, row.path), "current source remains the specified fixed product input");
    assert.deepEqual(inverseUI188CurrentSource(row.path, before, current), before);
    for (const witness of row.behaviorTests) assert.ok(existsSync(resolve(root, witness)), "registered behavior test source exists: " + witness);
  }
});

test("UI188-CONTRACT-002: missing, reverted, truncated, extra, unauthorized and wrong-original bytes cannot fall back to history", () => {
  for (const row of ui188CurrentContract.records) {
    const before = object(row.beforeCommit, row.path), current = historicalR4DeleteCopySource(row.path, historicalR5ReadinessSource(row.path, raw(row.path), object), object);
    for (const mutant of [Buffer.alloc(0), before, current.subarray(0, current.length - 1), Buffer.concat([current, Buffer.from("\n")])]) assert.throws(() => inverseUI188CurrentSource(row.path, before, mutant), /reviewed finite UI188 source bytes/);
    assert.throws(() => inverseUI188CurrentSource(row.path, Buffer.concat([before, Buffer.from("wrong")]), current), /fixed original hash/);
    assert.throws(() => inverseUI188CurrentSource("../unapproved", before, current), /unknown UI188 current source path/);
    const missing = Error("controlled immutable original missing");
    assert.throws(() => historicalUI188Source(row.path, current, () => { throw missing; }), error => error === missing);
  }
  const unlisted = Buffer.from("unlisted real source");
  assert.equal(historicalUI188Source("unlisted.ts", unlisted, () => { throw Error("unlisted must not read history"); }), unlisted);
});

test("UI188-CONTRACT-003: independent path/specification/witness registration cannot be removed, duplicated or retargeted", () => {
  const bytes = raw("web/tests/fixtures/ui-regression/ui188-current-source-deltas.json");
  assertUI188CurrentContract(bytes);
  for (const mutate of [
    (value: typeof ui188CurrentContract) => { (value.records as unknown[]).pop(); },
    (value: typeof ui188CurrentContract) => { (value.records as unknown[]).push(value.records[0]); },
    (value: typeof ui188CurrentContract) => { Object.assign(value.records[0], { path: "../unapproved" }); },
    (value: typeof ui188CurrentContract) => { Object.assign(value.records[0], { beforeSha256: "0".repeat(64) }); },
    (value: typeof ui188CurrentContract) => { Object.assign(value.records[0], { behaviorTests: [], specifications: [] }); },
  ]) {
    const value = structuredClone(ui188CurrentContract); mutate(value);
    assert.throws(() => assertUI188CurrentContract(Buffer.from(JSON.stringify(value, null, 2) + "\n")), /independent finite contract bytes changed/);
  }
  const parity = raw("web/tests/ui-regression/parity.test.mts").toString("utf8");
  const registration = 'import "./ui188-current-source-deltas.test.mts";';
  assertUI188ContractRegistration(parity);
  for (const mutant of [parity.replace(registration, ""), parity + "\n" + registration,
    parity.replace(registration, 'if (true) await import("./ui188-current-source-deltas.test.mts");'),
    parity.replace(registration, "/* " + registration + " */")]) {
    assert.throws(() => assertUI188ContractRegistration(mutant), /exactly once as a top-level side-effect import/);
  }
});
