import assert from "node:assert/strict";
import { createServer, type RequestListener } from "node:http";
import test from "node:test";
import {
  canInspectManualHold, createAutoStartResumeController, isAutoStartHoldSnapshot, projectHoldAttempt, resumeResultPresentation,
  type AutoStartHoldSnapshot, type AutoStartResumeRequest, type AutoStartResumeResponse,
} from "../src/features/streams/stream-auto-start-hold.ts";

const stream = { id: "msr-stream", name: "Synthetic stream", status: "ready", updated_at: "2026-11-16T01:00:00.000001Z", settings_revision: "settings-17", archive_run_id: "run-4", auto_start_trigger: "discord_voice_join" };
const hold = { stream_id: stream.id, known: true, held: true, revision: 7, reason: "manual_stop", actor_user_id: "operator", original_attempt_id: "stop-1", pending_attempt_id: "", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T01:00:00Z" };
const snapshot: AutoStartHoldSnapshot = { stream, hold };
const copy = <T,>(value: T): T => structuredClone(value);
const unavailable = { State: "UNAVAILABLE", Unavailable: { Code: "BOUNDARY_NOT_OBSERVED" } };
const context = { OwnerID: "cp-owner", Scope: stream.id, FrameRef: "frame-8", SourceRevision: 8 };
const fact = <T,>(value: T) => ({ State: "OBSERVED", Value: value, Observation: context });
const boundary = (value: string, kind: "hold" | "start" = "hold") => ({ Address: { Kind: kind === "hold" ? "CP_LIFECYCLE" : "PROJECTION", OwnerID: "cp-owner", Resource: { Kind: "STREAM", OwnerID: "cp-owner", ResourceID: stream.id }, SubBoundary: kind === "hold" ? "RECEIPT_SAVE" : "CP_OUTCOME" }, Knowledge: "KNOWN", Presence: "PRESENT", Outcome: fact(value) });
function response(request: AutoStartResumeRequest, condition = false, start: unknown = boundary("NO_EFFECT_PROVEN", "start")): AutoStartResumeResponse {
  const resultHold = { ...hold, held: false, revision: 9, original_attempt_id: request.request_id };
  return { stream, hold: resultHold, attempt: { request: { ...request, stream_id: stream.id, operation: "explicit_resume" }, hold: resultHold,
    condition: fact(condition), hold_commit: boundary("SUCCESS_AT_BOUNDARY"), start_result: start, gate: "NOT_QUALIFIED" } };
}
function barrier() { let release!: () => void; const wait = new Promise<void>(resolve => { release = resolve; }); return { wait, release }; }
async function httpFixture(t: { after(callback: () => unknown): void }, listener: RequestListener) {
  const server = createServer(listener);
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address === "object");
  return { server, origin: `http://127.0.0.1:${address.port}` };
}
function rig(overrides: Partial<Parameters<typeof createAutoStartResumeController>[0]> = {}) {
  let current = copy(snapshot), permissions = ["streams.start"], actorID = "operator", sessionMarker = "browser-session-a", sends = 0, gets = 0;
  const requests: AutoStartResumeRequest[] = [];
  const controller = createAutoStartResumeController({ streamID: stream.id, readSnapshot: async () => copy(current), readIdentity: async () => ({ actorID, permissions }),
    currentIdentity: () => ({ actorID, sessionMarker }), newRequestID: () => "resume-original-1", send: async request => { sends++; requests.push(request); return response(request); },
    inspect: async () => { gets++; return response(requests[0]); }, ...overrides });
  return { controller, get sends() { return sends; }, get gets() { return gets; }, requests, setSnapshot(value: AutoStartHoldSnapshot) { current = value; },
    setPermissions(value: string[]) { permissions = value; }, setActor(value: string) { actorID = value; }, setSession(value: string) { sessionMarker = value; } };
}
async function open(r: ReturnType<typeof rig>) { const value = await r.controller.open(); assert.equal(value.kind, "opened"); if (value.kind !== "opened") throw Error("expected confirmation"); return value.opened; }

test("MSR-UI-01: confirmation binds the detached original hold, Run and configuration", async t => {
  for (const change of [
    (value: AutoStartHoldSnapshot) => ({ ...value, hold: { ...value.hold, revision: 8 } }),
    (value: AutoStartHoldSnapshot) => ({ ...value, stream: { ...value.stream, settings_revision: "settings-18" } }),
    (value: AutoStartHoldSnapshot) => ({ ...value, stream: { ...value.stream, archive_run_id: "run-5" } }),
    (value: AutoStartHoldSnapshot) => ({ ...value, stream: { ...value.stream, updated_at: "2026-11-16T02:00:00Z" } }),
  ]) await t.test("changed authority sends zero resume mutations", async () => {
    const r = rig(), opened = await open(r); r.setSnapshot(change(copy(snapshot)));
    assert.deepEqual(await r.controller.confirm(opened), { kind: "blocked", reason: "changed" }); assert.equal(r.sends, 0);
  });
  await t.test("mutating the caller snapshot cannot rewrite an opened request", async () => {
    const mutable = copy(snapshot), r = rig({ readSnapshot: async () => mutable }), opened = await open(r);
    assert.ok(Object.isFrozen(opened.snapshot) && Object.isFrozen(opened.snapshot.stream) && Object.isFrozen(opened.snapshot.hold));
    mutable.hold = { ...mutable.hold, revision: 99 };
    assert.equal(opened.snapshot.hold.revision, 7);
    assert.equal((await r.controller.confirm(opened)).kind, "blocked"); assert.equal(r.sends, 0);
  });
  await t.test("a wrong Stream is never accepted as the selected target", async () => {
    assert.equal(isAutoStartHoldSnapshot({ ...snapshot, stream: { ...stream, id: "other" } }, stream.id), false);
    assert.equal(isAutoStartHoldSnapshot({ ...snapshot, hold: { ...hold, stream_id: "other" } }, stream.id), false);
  });
  await t.test("a server-owned pending Hold is not silently treated as a persisted Resume", async () => {
    const r = rig(); r.setSnapshot({ stream, hold: { ...hold, pending_attempt_id: "stop-pending" } });
    assert.deepEqual(await r.controller.open(), { kind: "blocked", reason: "pending" });
    assert.equal(r.controller.pendingRequestID, undefined); assert.equal(r.sends, 0);
    assert.equal((await r.controller.inspect()).kind, "blocked"); assert.equal(r.gets, 0);
  });
});

test("MSR-UI-02: fresh permission, Session and cancellation control the actual mutation", async t => {
  for (const permissions of [[], ["streams.stop"], ["streams.start"], ["*"]]) await t.test(`permission ${permissions.join(",") || "none"}`, async () => {
    const r = rig(); r.setPermissions(permissions); const opened = await r.controller.open();
    assert.equal(opened.kind, permissions.includes("*") || permissions.includes("streams.start") ? "opened" : "blocked"); assert.equal(r.sends, 0);
  });
  await t.test("revocation after confirmation display is not a POST", async () => {
    const r = rig(), opened = await open(r); r.setPermissions([]);
    assert.deepEqual(await r.controller.confirm(opened), { kind: "blocked", reason: "permission" }); assert.equal(r.sends, 0);
  });
  await t.test("a new browser Session cannot use an old confirmation", async () => {
    const r = rig(), opened = await open(r); r.setSession("browser-session-b");
    assert.deepEqual(await r.controller.confirm(opened), { kind: "blocked", reason: "changed" }); assert.equal(r.sends, 0);
  });
  await t.test("cleanup invalidates the old confirmation while a newly opened confirmation remains usable", async () => {
    const r = rig(), old = await open(r); r.controller.cancel();
    assert.deepEqual(await r.controller.confirm(old), { kind: "blocked", reason: "changed" }); assert.equal(r.sends, 0);
    const fresh = await open(r);
    assert.equal((await r.controller.confirm(fresh)).kind, "observed"); assert.equal(r.sends, 1);
  });
  await t.test("cancel during held fresh reads sends no mutation", async () => {
    const entered = barrier(), released = barrier(); let calls = 0, sends = 0;
    const r = rig({ readSnapshot: async () => { if (++calls > 1) { entered.release(); await released.wait; } return copy(snapshot); }, send: async req => { sends++; return response(req); } });
    const opened = await open(r), pending = r.controller.confirm(opened); await entered.wait; r.controller.cancel(); released.release();
    assert.equal((await pending).kind, "blocked"); assert.equal(sends, 0);
  });
});

test("MSR-UI-03: real HTTP ACK loss retains one original request and only inspects it", async t => {
  let posts = 0, inspections = 0, stored: AutoStartResumeRequest | undefined;
  const { origin } = await httpFixture(t, async (req, res) => {
    res.setHeader("Content-Type", "application/json");
    if (req.method === "POST") {
      posts++; let body = ""; for await (const part of req) body += part;
      stored = JSON.parse(body); res.destroy(); return;
    }
    if (req.url?.includes("resume-attempts")) { inspections++; assert.ok(stored); res.end(JSON.stringify(response(stored))); return; }
    res.end(JSON.stringify(snapshot));
  });
  const r = rig({ readSnapshot: async signal => (await fetch(origin + "/streams/" + stream.id + "/auto-start-hold", { signal })).json(),
    send: async (request, signal) => { const value = await fetch(origin + "/streams/" + stream.id + "/auto-start/resume", { method: "POST", signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify(request) }); return value.json(); },
    inspect: async (id, signal) => (await fetch(origin + "/streams/" + stream.id + "/auto-start/resume-attempts/" + id, { signal })).json() });
  const opened = await open(r), result = await r.controller.confirm(opened);
  assert.deepEqual(result, { kind: "unknown", requestID: "resume-original-1" }); assert.equal(posts, 1);
  assert.equal((await r.controller.open()).kind, "blocked"); assert.equal((await r.controller.confirm(opened)).kind, "blocked"); assert.equal(posts, 1);
  const observed = await r.controller.inspect(); assert.equal(observed.kind, "observed");
  if (observed.kind !== "observed") throw Error("expected original result");
  assert.equal(observed.requestID, stored?.request_id); assert.equal(observed.presentation.holdCommit, "known");
  assert.equal(observed.presentation.condition, "not-met"); assert.equal(observed.presentation.start, "not-started"); assert.equal(observed.unresolved, false);
  assert.deepEqual([posts, inspections], [1, 1]);
});

test("MSR-UI-04: duplicate submits and changed same-ID receipts cannot become another resume", async t => {
  await t.test("a pending transport allows one POST", async () => {
    const entered = barrier(), released = barrier(); let sends = 0;
    const r = rig({ send: async req => { sends++; entered.release(); await released.wait; return response(req); } });
    const opened = await open(r), pending = r.controller.confirm(opened); await entered.wait;
    assert.equal((await r.controller.confirm(opened)).kind, "blocked"); assert.equal((await r.controller.open()).kind, "blocked");
    released.release(); assert.equal((await pending).kind, "observed"); assert.equal(sends, 1);
  });
  await t.test("same ID with different original content remains UNKNOWN", async () => {
    let post = 0, inspect = 0;
    const r = rig({ send: async req => { post++; return response({ ...req, expected_settings_revision: "different-original" }); }, inspect: async () => { inspect++; return { stream, hold }; } });
    const opened = await open(r); assert.equal((await r.controller.confirm(opened)).kind, "unknown");
    assert.equal((await r.controller.inspect()).kind, "unknown"); assert.equal((await r.controller.confirm(opened)).kind, "blocked");
    assert.deepEqual([post, inspect], [1, 1]);
  });
  await t.test("a generic DB hold result is not an execution receipt", async () => {
    const r = rig({ send: async () => ({ stream, hold: { ...hold, held: false, revision: 9 } }) });
    assert.equal((await r.controller.confirm(await open(r))).kind, "unknown"); assert.equal(r.controller.pendingRequestID, "resume-original-1");
  });
  await t.test("a non-string gate remains UNKNOWN after dispatch without coercing untrusted JSON", async () => {
    const r = rig({ send: async request => { const value = response(request); return { ...value, attempt: { ...value.attempt, gate: { toString: null, valueOf: null } } }; } });
    assert.equal((await r.controller.confirm(await open(r))).kind, "unknown");
    assert.equal(r.controller.pendingRequestID, "resume-original-1");
  });
});

test("MSR-UI-05: CP commit, condition and external start are independent knowledge", () => {
  const req = { request_id: "resume-original-1", expected_hold_revision: 7, expected_stream_revision: stream.updated_at, expected_settings_revision: stream.settings_revision, expected_run_id: stream.archive_run_id };
  const value = response(req, true, { Knowledge: "UNKNOWN", Presence: "NOT_OBSERVED", Outcome: unavailable });
  assert.deepEqual(resumeResultPresentation(value, stream.id), { holdCommit: "known", condition: "met", start: "unknown", gate: "NOT_QUALIFIED" });
  assert.equal(resumeResultPresentation({ ...value, attempt: { ...value.attempt, condition: unavailable } }, stream.id).condition, "unknown");
  assert.equal(resumeResultPresentation({ ...value, attempt: { ...value.attempt, hold_commit: { ...boundary("SUCCESS_AT_BOUNDARY"), Knowledge: "UNKNOWN" } } }, stream.id).holdCommit, "unknown");
  assert.equal(resumeResultPresentation(response(req, false), stream.id).start, "not-started");
  assert.equal(resumeResultPresentation(response(req, true, boundary("SUCCESS_AT_BOUNDARY")), stream.id).start, "unknown", "a Hold commit cannot prove Start acceptance");
  assert.equal(resumeResultPresentation({ ...value, attempt: { ...value.attempt, hold_commit: boundary("SUCCESS_AT_BOUNDARY", "start") } }, stream.id).holdCommit, "unknown", "a Start projection cannot prove a Hold commit");
  const mixedOwner = boundary("SUCCESS_AT_BOUNDARY"); mixedOwner.Address.Resource.OwnerID = "other-owner";
  assert.equal(resumeResultPresentation({ ...value, attempt: { ...value.attempt, hold_commit: mixedOwner } }, stream.id).holdCommit, "unknown");
});

test("MSR-UI-06: a persisted original ID permits only authorized read recovery", async () => {
  let posts = 0, gets = 0;
  const req = { request_id: "resume-original-1", expected_hold_revision: 7, expected_stream_revision: stream.updated_at, expected_settings_revision: stream.settings_revision, expected_run_id: stream.archive_run_id };
  const r = rig({ send: async () => { posts++; throw Error("not expected"); }, inspect: async id => { gets++; assert.equal(id, req.request_id); return response(req); } });
  r.controller.recover(req.request_id); assert.equal((await r.controller.open()).kind, "blocked");
  r.setPermissions([]); assert.equal((await r.controller.inspect()).kind, "blocked"); assert.equal(gets, 0);
  r.setPermissions(["streams.start"]); assert.equal((await r.controller.inspect()).kind, "observed"); assert.deepEqual([posts, gets], [0, 1]);
});

test("MSR-UI-09: generic Hold inspection is read scoped and never proves physical Stop or Start", () => {
  assert.equal(canInspectManualHold({ actorID: "reader", permissions: ["streams.read"] }), true);
  assert.equal(canInspectManualHold({ actorID: "starter", permissions: ["streams.start"] }), false);
  assert.equal(canInspectManualHold({ actorID: "operator", permissions: ["*"] }), true);
  for (const operation of ["manual_stop", "manual_force_stop", "explicit_resume"] as const) {
    const historical = { ...hold, held: operation !== "explicit_resume", revision: 8 };
    const value = { stream, hold: { ...hold, held: !historical.held, revision: 12 }, attempt: { request: { request_id: "original-held-1", stream_id: stream.id, operation }, hold: historical, hold_commit: boundary("SUCCESS_AT_BOUNDARY"), gate: "HELD" } };
    const projected = projectHoldAttempt(value, stream.id, "original-held-1"); assert.ok(projected);
    assert.equal(projected.holdCommit, "known"); assert.equal(projected.historicalHold.held, historical.held);
    assert.equal(projected.historicalHold.revision, 8); assert.equal(projected.physicalResult, "unknown"); assert.equal(projected.gate, "HELD");
    assert.equal(projectHoldAttempt(value, "other", "original-held-1"), undefined);
    assert.equal(projectHoldAttempt(value, stream.id, "different"), undefined);
    assert.equal(projectHoldAttempt({ ...value, attempt: { ...value.attempt, request: { ...value.attempt.request, operation: "future" } } }, stream.id, "original-held-1"), undefined);
  }
});
