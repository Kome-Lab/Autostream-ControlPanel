import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import test from "node:test";
import { QueryClient } from "@tanstack/react-query";
import type { Stream, WorkerNode } from "../src/types/domain.ts";
import type { StreamVisualSettings, VideoCoverState } from "../src/features/streams/control-platform.ts";
import { actualCallback, actualEffect, actualFunction, actualJSXCallback } from "./ui-regression/source-callback.mts";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");
register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url, data: { webRootURL: new URL("../", import.meta.url).href },
});
const { buildVideoCoverAction, createVideoCoverActionController } = await import("../src/features/streams/control-platform.ts");
const { withBoundedActionRevalidation } = await import("../src/lib/foundation/actions/bounded-revalidation.ts");
const source = () => readFileSync(new URL("../src/features/streams/stream-control-platform-panel.tsx", import.meta.url), "utf8");

function fixture() {
  const queryClient = new QueryClient();
  const stream: Stream = { id: "stream-live", name: "Live fixture", status: "live", assigned_encoder_id: "encoder-one" };
  const user = { permissions: ["streams.show_cover", "streams.hide_cover"] };
  const encoder: WorkerNode = { id: "encoder-one", service_id: "encoder-one", service_type: "encoder_recorder", service_name: "Fixture Encoder", status: "online", current_stream_id: stream.id, assignment_role: "primary", ownership_epoch: 1, execution_host_id: "host-one", reported_capabilities: { live_video_cover_v1: true } };
  const visual: StreamVisualSettings = { stream_id: stream.id, revision: 7, background_mode: "default", header_title_mode: "default", discord_snapshot_revision: 0, cover_source: "upload", cover_asset_id: "asset-one", cover_variant_id: "variant-one", cover_start_active: false, cover_candidates: [{ name: "Cover one", source: "upload", asset_id: "asset-one", variant_id: "variant-one" }] };
  const cover: VideoCoverState = { stream_id: stream.id, job_generation: 3, desired_revision: 4, desired_active: false, applied_revision: 4, applied_active: false, status: "applied", pipeline_order: [], cover_watermark_independent: true };
  queryClient.setQueryData(["auth", "me"], user);
  queryClient.setQueryData(["streams"], [stream]);
  queryClient.setQueryData(["service-health"], [encoder]);
  queryClient.setQueryData(["streams", stream.id, "visual-settings"], visual);
  queryClient.setQueryData(["streams", stream.id, "video-cover-state"], cover);
  const reads: string[] = [], writes: Record<string, unknown>[] = [];
  const responses = new Map<string, unknown>([["/auth/me", user], [`/streams/${stream.id}`, stream], ["/service-health", [encoder]], [`/streams/${stream.id}/visual-settings`, visual], [`/streams/${stream.id}/video-cover-state`, cover]]);
  let writeError: Error | undefined;
  const text = source();
  const coverCapabilityFromRows = actualFunction(text, "coverCapabilityFromRows", {});
  const videoCoverEncoderAuthority = actualFunction(text, "videoCoverEncoderAuthority", {});
  const captureVideoCoverAuthority = actualFunction(text, "captureVideoCoverAuthority", { videoCoverEncoderAuthority });
  const videoCoverCapabilitySnapshot = actualFunction(text, "videoCoverCapabilitySnapshot", { coverCapabilityFromRows });
  const permissionSnapshot = actualCallback(text, "permissionSnapshot", { queryClient });
  const capabilitySnapshot = actualCallback(text, "capabilitySnapshot", { queryClient, stream, streamRef: { current: stream }, videoCoverCapabilitySnapshot });
  const apiGet = async (path: string, signal?: AbortSignal) => {
    assert.equal(signal?.aborted, false);
    reads.push(path);
    assert.equal(responses.has(path), true, "only relevant real authority endpoints may be read");
    return structuredClone(responses.get(path));
  };
  // Before the implementation the actual caller has no revalidation function.
  // Its absence is not skipped: every save-time test below asserts the reads
  // and zero-write effects, so the old caller fails through its real path.
  const revalidateVideoCoverAuthority = /function revalidateVideoCoverAuthority\(/.test(text)
    ? actualFunction(text, "revalidateVideoCoverAuthority", { apiGet, coverCapabilityFromRows, captureVideoCoverAuthority, videoCoverEncoderAuthority }) : undefined;
  const controller = actualCallback(text, "controller", {
    useMemo: (factory: () => unknown) => factory(), createVideoCoverActionController,
    permissionSnapshot, capabilitySnapshot, queryClient, stream, streamRef: { current: stream },
    revalidateVideoCoverAuthority, withBoundedActionRevalidation, captureVideoCoverAuthority,
    apiPut: async (_path: string, request: Record<string, unknown>, signal?: AbortSignal) => {
      assert.notEqual(signal?.aborted, true); writes.push(request);
      if (writeError) throw writeError;
      return { ...cover, desired_revision: 5, desired_active: request.active === true, asset_variant_id: request.active === true ? "variant-one" : undefined };
    },
  });
  const request = { active: true, expected_job_generation: 3, expected_revision: 4, idempotency_key: "cover-once", candidate_variant_id: "variant-one", expected_visual_revision: 7 };
  return { queryClient, stream, user, encoder, visual, cover, reads, writes, responses, controller, request, permissionSnapshot, capabilitySnapshot, captureVideoCoverAuthority,
    failWrite(error: Error) { writeError = error; },
    refreshing(key: readonly string[]) { const query = queryClient.getQueryCache().find({ queryKey: key, exact: true }); assert.ok(query); query.setState({ fetchStatus: "fetching" }); },
    close() { queryClient.clear(); },
  };
}

test("POLL-COVER-001: the actual panel retains successful permissions during background auth GET", () => {
  const f = fixture();
  try { f.refreshing(["auth", "me"]); assert.deepEqual(f.permissionSnapshot(), { status: "ready", permissions: f.user.permissions }); }
  finally { f.close(); }
});

test("POLL-COVER-002: the actual panel retains Encoder capability while the same health data refreshes", () => {
  const f = fixture();
  try { f.refreshing(["service-health"]); assert.deepEqual(f.capabilitySnapshot(), { status: "ready", supported: true }); }
  finally { f.close(); }
});

test("POLL-COVER-003: actual caller rechecks permission and never sends a revoked cover request", async () => {
  const f = fixture();
  try { f.responses.set("/auth/me", { permissions: [] }); const result = await f.controller.issueRequest(f.request); assert.equal(result.sent, false); assert.equal(f.writes.length, 0); assert.ok(f.reads.includes("/auth/me")); }
  finally { f.close(); }
});

test("POLL-COVER-004: actual caller rejects a changed assigned Encoder before PUT", async () => {
  const f = fixture();
  try { f.responses.set(`/streams/${f.stream.id}`, { ...f.stream, assigned_encoder_id: "other-encoder" }); const result = await f.controller.issueRequest(f.request); assert.equal(result.sent, false); assert.equal(f.writes.length, 0); }
  finally { f.close(); }
});

test("POLL-COVER-005: actual caller rejects a changed visual revision before PUT", async () => {
  const f = fixture();
  try { f.responses.set(`/streams/${f.stream.id}/visual-settings`, { ...f.visual, revision: 8 }); const result = await f.controller.issueRequest(f.request); assert.equal(result.sent, false); assert.equal(f.writes.length, 0); }
  finally { f.close(); }
});

test("POLL-COVER-006: one intent uses a bounded fresh local proof across observer fetches and issues one PUT", async () => {
  const f = fixture();
  try {
    f.refreshing(["auth", "me"]); f.refreshing(["service-health"]);
    const result = await f.controller.issueRequest(f.request);
    assert.equal(result.sent, true); assert.equal(f.writes.length, 1);
    for (const path of ["/auth/me", `/streams/${f.stream.id}`, `/streams/${f.stream.id}/visual-settings`, "/service-health"]) assert.ok(f.reads.includes(path), path);
    assert.equal(f.writes[0].expected_revision, 4); assert.equal(f.writes[0].expected_visual_revision, 7);
    assert.equal(f.writes[0].idempotency_key, "cover-once");
  } finally { f.close(); }
});

test("POLL-COVER-007: an unknown issued request remains latched after a generic GET refresh", async () => {
  const f = fixture();
  try {
    f.failWrite(new TypeError("Response lost"));
    await assert.rejects(f.controller.issueRequest(f.request));
    assert.equal(f.controller.unresolved, true); f.controller.reconcile();
    assert.equal(f.controller.unresolved, true);
    assert.equal((await f.controller.issueRequest(f.request)).sent, false); assert.equal(f.writes.length, 1);
  } finally { f.close(); }
});

test("POLL-COVER-008: hide still requires explicit confirmation and never sends on the unconfirmed path", async () => {
  const f = fixture();
  try {
    assert.equal((await f.controller.issueRequest({ ...f.request, active: false, hide_confirmed: false })).sent, false);
    assert.equal(f.writes.length, 0);
  } finally { f.close(); }
});

test("POLL-COVER-009: actual caller rejects true epoch, capability, job, and desired-state changes", async () => {
  for (const change of ["epoch", "capability", "job", "revision", "confirming"] as const) {
    const f = fixture();
    try {
      if (change === "epoch") f.responses.set("/service-health", [{ ...f.encoder, ownership_epoch: 2 }]);
      if (change === "capability") f.responses.set("/service-health", [{ ...f.encoder, reported_capabilities: { live_video_cover_v1: false } }]);
      if (change === "job") f.responses.set(`/streams/${f.stream.id}/video-cover-state`, { ...f.cover, job_generation: 4 });
      if (change === "revision") f.responses.set(`/streams/${f.stream.id}/video-cover-state`, { ...f.cover, desired_revision: 5 });
      if (change === "confirming") f.responses.set(`/streams/${f.stream.id}/video-cover-state`, { ...f.cover, status: "confirming" });
      assert.equal((await f.controller.issueRequest(f.request)).sent, false, change);
      assert.equal(f.writes.length, 0, change);
    } finally { f.close(); }
  }
});

function receiptFixture(f: ReturnType<typeof fixture>) {
  const notices: string[] = [], reconciliation: boolean[] = [];
  const streamRef = { current: f.stream }, controllerRef = { current: f.controller as typeof f.controller | null };
  const bindings = {
    useMutation: (options: unknown) => options, controller: f.controller, stream: f.stream, queryClient: f.queryClient,
    streamRef, controllerRef, locale: "ja", t: (key: string) => key,
    setNotice: (value: string) => notices.push(value), setNeedsReconciliation: (value: boolean) => reconciliation.push(value),
    CoverActionUnavailable: class extends Error {}, fixedPresentationText: (value: string) => value,
    uiText: (value: string) => value, adaptAPIError: () => ({ messageKey: "unknown" }),
  };
  const action = actualCallback(source(), "action", bindings);
  const input = { request: f.request, streamID: f.stream.id, owner: f.controller };
  return { bindings, action, input, streamRef, controllerRef, notices, reconciliation };
}

test("POLL-COVER-010: only the issuing owner may publish success or failure, including A to B to A", async () => {
  for (const switchKind of ["different-target", "same-target-new-owner", "unmounted"] as const) {
    const f = fixture(), other = fixture();
    try {
      const r = receiptFixture(f);
      r.streamRef.current = switchKind === "different-target" ? { ...f.stream, id: "stream-other" } : f.stream;
      r.controllerRef.current = switchKind === "unmounted" ? null : other.controller;
      await assert.rejects(r.action.mutationFn(r.input), "a retired owner may not start a new request");
      assert.deepEqual(f.reads, []); assert.deepEqual(f.writes, []);
      r.action.onMutate(r.input);
      r.action.onError(new TypeError("Late response loss"), r.input);
      await r.action.onSuccess({ ...f.cover, desired_revision: 5, desired_active: true, status: "applied" }, r.input);
      assert.deepEqual(r.notices, []); assert.deepEqual(r.reconciliation, []);
      assert.equal(f.queryClient.getQueryData<VideoCoverState>(["streams", f.stream.id, "video-cover-state"])?.desired_revision, 4);
      assert.equal(f.controller.unresolved, true, "the old owner retains its own uncertain request");
      assert.equal(other.controller.unresolved, false);
    } finally { f.close(); other.close(); }
  }
  const f = fixture();
  try {
    const r = receiptFixture(f), accepted = { ...f.cover, desired_revision: 5, desired_active: true, asset_variant_id: "variant-one", status: "confirming" };
    await r.action.onSuccess(accepted, r.input);
    assert.deepEqual(r.reconciliation, [true]); assert.equal(f.controller.unresolved, true);
    assert.equal(f.queryClient.getQueryData<VideoCoverState>(["streams", f.stream.id, "video-cover-state"])?.desired_revision, 5);
  } finally { f.close(); }
});

test("POLL-COVER-011: a held read-only refresh cannot update a new owner but resolves its own accepted receipt", async () => {
  for (const switchOwner of [true, false]) {
    const f = fixture(), other = fixture();
    try {
      const r = receiptFixture(f);
      const accepted: VideoCoverState = { ...f.cover, desired_revision: 5, desired_active: false, status: "confirming" };
      f.controller.holdForReconciliation(accepted);
      let release!: (result: unknown) => void;
      const held = new Promise((resolve) => { release = resolve; });
      const refresh = actualCallback(source(), "refresh", { ...r.bindings, cover: { refetch: () => held }, visual: { refetch: async () => ({}) }, serviceHealth: { refetch: async () => ({}) } });
      const reading = refresh();
      if (switchOwner) r.controllerRef.current = other.controller;
      release({ isSuccess: true, data: { ...accepted, applied_active: false, applied_revision: 5, status: "applied" } });
      await reading;
      assert.deepEqual(r.reconciliation, switchOwner ? [] : [false]);
      assert.equal(r.notices.length, switchOwner ? 0 : 1);
      assert.equal(f.controller.unresolved, switchOwner);
      assert.equal(other.controller.unresolved, false);
    } finally { f.close(); other.close(); }
  }
});

function hideFixture(f: ReturnType<typeof fixture>, observedCover = f.cover) {
  let opened = false, keys = 0;
  const notices: string[] = [], inputs: Record<string, unknown>[] = [], outcomes: Promise<unknown>[] = [];
  const hideIntent = { current: null as Record<string, unknown> | null };
  const r = receiptFixture(f);
  const bindings = {
    queryClient: f.queryClient, stream: f.stream, controller: f.controller, streamRef: r.streamRef, controllerRef: r.controllerRef,
    cover: { data: structuredClone(observedCover) }, visual: { data: structuredClone(f.visual) }, serviceHealth: { data: [structuredClone(f.encoder)] },
    coverRuntimeReady: actualCallback(source(), "coverRuntimeReady", { cover: { data: observedCover } }), reconcileRequired: false, coverPending: false, hide: { allowed: true }, hideIntent,
    captureVideoCoverAuthority: f.captureVideoCoverAuthority, buildVideoCoverAction,
    newVideoCoverIdempotencyKey: () => `fixed-hide-${++keys}`,
    setHideOpen: (value: boolean) => { opened = value; }, setNotice: (value: string) => notices.push(value), t: (key: string) => key,
    action: { mutate(input: Record<string, unknown>) { inputs.push(input); outcomes.push(r.action.mutationFn(input)); } },
  };
  const openHide = actualCallback(source(), "openHide", bindings), confirmHide = actualCallback(source(), "confirmHide", bindings);
  return { bindings, inputs, outcomes, notices, hideIntent,
    open: (open: boolean) => actualJSXCallback(source(), "ConfirmationDialogFrame", "onOpenChange", { openHide })(open),
    confirm: () => actualJSXCallback(source(), "ConfirmationDialogFrame", "onConfirm", { confirmHide })(),
    get opened() { return opened; }, get keys() { return keys; },
  };
}

test("POLL-COVER-012: actual Hide JSX pins open-time authority and rejects poll changes without rebasing", async () => {
  for (const change of ["same", "visual", "job", "epoch"] as const) {
    const f = fixture();
    try {
      const h = hideFixture(f); h.open(true);
      assert.equal(h.opened, true); assert.ok(h.hideIntent.current);
      const captured = h.hideIntent.current;
      assert.equal(Object.isFrozen(captured), true); assert.equal(Object.isFrozen(captured.baseline), true);
      if (change === "visual") {
        const next = { ...f.visual, revision: 8 }; h.bindings.visual.data = next;
        f.queryClient.setQueryData(["streams", f.stream.id, "visual-settings"], next); f.responses.set(`/streams/${f.stream.id}/visual-settings`, next);
      }
      if (change === "job") {
        const next = { ...f.cover, job_generation: 4, desired_revision: 5 }; h.bindings.cover.data = next;
        f.queryClient.setQueryData(["streams", f.stream.id, "video-cover-state"], next); f.responses.set(`/streams/${f.stream.id}/video-cover-state`, next);
      }
      if (change === "epoch") {
        const next = { ...f.encoder, ownership_epoch: 2 }; h.bindings.serviceHealth.data = [next];
        f.queryClient.setQueryData(["service-health"], [next]); f.responses.set("/service-health", [next]);
      }
      h.confirm(); assert.equal(h.inputs.length, 1); assert.equal(h.keys, 1); assert.equal(h.opened, false);
      assert.equal(h.inputs[0], captured, "Confirm must submit the immutable open-time intent itself");
      if (change === "same") {
        await h.outcomes[0]; assert.equal(f.writes.length, 1); assert.equal(f.writes[0].active, false); assert.equal(f.writes[0].hide_confirmed, true);
        assert.equal(f.writes[0].expected_job_generation, 3); assert.equal(f.writes[0].expected_revision, 4); assert.equal(f.writes[0].idempotency_key, "fixed-hide-1");
        assert.equal("baseline" in f.writes[0], false); assert.equal("owner" in f.writes[0], false);
      } else { await assert.rejects(h.outcomes[0]); assert.equal(f.writes.length, 0, change); }
      assert.equal(f.controller.unresolved, false, "a rejected preflight never becomes an uncertain issued write");
    } finally { f.close(); }
  }
});

test("POLL-COVER-013: Hide cancellation and owner replacement discard the old dialog intent", () => {
  const key = (stream: Stream) => actualJSXCallback(source(), "StreamControlPlatformPanelContent", "key", { stream });
  const target: Stream = { id: "stream-live", name: "Original", status: "live", assigned_encoder_id: "encoder-one" };
  assert.equal(key(target), key({ ...target, name: "Polled name", status: "stopping", assigned_encoder_id: "encoder-other" }), "same target retains its unresolved owner across authority changes");
  assert.notEqual(key(target), key({ ...target, id: "other-stream" }), "a different target must receive a separate component lifetime");
  for (const replacement of ["cancel", "different-target", "same-target-new-owner"] as const) {
    const f = fixture(), other = fixture();
    try {
      const h = hideFixture(f); h.open(true); assert.equal(h.opened, true);
      if (replacement === "cancel") h.open(false);
      else {
        h.bindings.controllerRef.current = other.controller;
        if (replacement === "different-target") h.bindings.streamRef.current = { ...f.stream, id: "other-stream" };
        const effect = actualEffect(source(), "hideIntent.current = null", { ...h.bindings, controller: other.controller }, "useLayoutEffect");
        const cleanup = effect(); cleanup();
      }
      h.confirm(); assert.equal(h.opened, false); assert.equal(h.hideIntent.current, null); assert.deepEqual(h.inputs, []); assert.equal(f.writes.length, 0);
    } finally { f.close(); other.close(); }
  }
});

test("POLL-COVER-014: a pending mutation belongs only to its issuing target and owner", () => {
  const f = fixture(), other = fixture();
  try {
    const action = { isPending: true, variables: { streamID: f.stream.id, owner: f.controller } };
    assert.equal(actualCallback(source(), "coverPending", { action, controller: f.controller, stream: f.stream }), true);
    assert.equal(actualCallback(source(), "coverPending", { action, controller: f.controller, stream: { ...f.stream, id: "other-stream" } }), false);
    assert.equal(actualCallback(source(), "coverPending", { action, controller: other.controller, stream: f.stream }), false);
  } finally { f.close(); other.close(); }
});

test("POLL-COVER-015: removed explicit cover selection stays missing and cannot silently Show a fallback", () => {
  const f = fixture();
  try {
    for (const candidates of [f.visual.cover_candidates || [], []]) {
      const selectedCandidate = "removed-variant";
      const selectedCandidateMissing = actualCallback(source(), "selectedCandidateMissing", { selectedCandidate, candidates });
      const candidateID = actualCallback(source(), "candidateID", { selectedCandidate, candidates, cover: { data: f.cover }, visual: { data: f.visual } });
      assert.equal(selectedCandidateMissing, true); assert.equal(candidateID, selectedCandidate);
      let requests = 0;
      const issue = actualCallback(source(), "issue", { cover: { data: f.cover }, coverRuntimeReady: true, reconcileRequired: false, coverPending: false, selectedCandidateMissing, action: { mutate() { requests++; } } });
      issue(true); assert.equal(requests, 0); assert.equal(f.writes.length, 0);
    }
    const selectedCandidate = "variant-one", candidates = f.visual.cover_candidates || [];
    assert.equal(actualCallback(source(), "selectedCandidateMissing", { selectedCandidate, candidates }), false, "only explicit reselection resolves the missing selection");
    assert.equal(actualCallback(source(), "candidateID", { selectedCandidate, candidates, cover: { data: f.cover }, visual: { data: f.visual } }), "variant-one");
  } finally { f.close(); }
});

test("POLL-COVER-016: zero-generation pre-start Show and Hide never send or latch an uncertain write", async () => {
  const f = fixture();
  const h = hideFixture(f, { ...f.cover, job_generation: 0, desired_revision: 0 });
  try {
    assert.equal(h.bindings.coverRuntimeReady, false);
    const issue = actualCallback(source(), "issue", { ...h.bindings, selectedCandidateMissing: false });
    issue(true); h.open(true); h.confirm();
    await Promise.allSettled(h.outcomes);
    assert.equal(h.opened, false); assert.deepEqual(h.inputs, []); assert.deepEqual(f.reads, []); assert.deepEqual(f.writes, []); assert.equal(f.controller.unresolved, false);
  } finally { await Promise.allSettled(h.outcomes); f.close(); }
});
