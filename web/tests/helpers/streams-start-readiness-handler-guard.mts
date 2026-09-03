import assert from "node:assert/strict";

export type StreamsStartReadinessGuardSources = Readonly<{
  view: string;
  controller: string;
  descriptors: string;
}>;

export type StreamsStartReadinessGuardMutation =
  | "remove-current-permission-snapshot"
  | "use-streams-update-authority"
  | "remove-pre-submit-evaluation"
  | "move-mutation-before-guard"
  | "add-alternate-unguarded-mutation";

const permissionSnapshotWiring = "getPermissions: () => streamPermissionSnapshot(queryClient)";
const preSubmitEvaluation = "const reason = evaluationReason(evaluateIgnoringPending(opened.intent));";
const guardedMutation = "const value = await dependencies.mutate(Object.freeze({ ...requestValue, signal: operation.abort.signal }));";

export function assertStreamsStartReadinessHandlerGuard(sources: StreamsStartReadinessGuardSources) {
  assert.equal(
    matchCount(sources.view, /intent=\{\{\s*id:\s*"STR-08"/g),
    1,
    "Streams view must expose exactly one STR-08 control",
  );
  assert.equal(
    matchCount(sources.view, /getPermissions:\s*\(\)\s*=>\s*streamPermissionSnapshot\(queryClient\)/g),
    1,
    "Streams controller wiring must read the current permission snapshot",
  );
  assert.doesNotMatch(
    sources.view,
    /\b(?:apiGet|apiPost|apiPut|apiDelete|fetch)\s*\([^\n]*start-readiness/,
    "Streams view must not add an alternate start-readiness transport path",
  );

  assert.match(
    sources.descriptors,
    /template\("STR-08",\s*"guarded",\s*"streams\.start",\s*"POST",\s*"none",\s*Object\.freeze\(\{\s*kind:\s*"manual-after-refresh"/,
    "STR-08 descriptor authority must remain streams.start with manual-after-refresh retry",
  );
  assert.match(
    sources.descriptors,
    /case\s+"STR-08":\s*return\s+encoded\s*\?\s*request\(intent\.id,\s*"POST",\s*`\/streams\/\$\{encoded\}\/start-readiness`\)/,
    "STR-08 must retain its exact POST start-readiness request",
  );

  const submitStart = sources.controller.indexOf("const submit = async");
  const submitEnd = sources.controller.indexOf("function acquire", submitStart);
  assert.ok(submitStart >= 0 && submitEnd > submitStart, "stream controller submit boundary missing");
  const submit = sources.controller.slice(submitStart, submitEnd);
  const acquireIndex = submit.indexOf("const operation = acquire(scope);");
  const evaluationIndex = submit.indexOf(preSubmitEvaluation);
  const stateIndex = submit.indexOf("const state = dependencies.getState(opened.intent);");
  const fingerprintIndex = submit.indexOf("if (state.fingerprint !== opened.authority)");
  const requestIndex = submit.indexOf("const requestValue = streamActionRequest(opened.intent);");
  const mutationIndex = submit.indexOf("dependencies.mutate(");
  assert.ok(acquireIndex >= 0, "pre-submit duplicate lock missing");
  assert.ok(evaluationIndex > acquireIndex, "pre-submit evaluator must run after acquiring the duplicate lock");
  assert.ok(stateIndex > evaluationIndex, "fresh state must be read after pre-submit permission evaluation");
  assert.ok(fingerprintIndex > stateIndex, "authority fingerprint guard must follow the fresh state read");
  assert.ok(requestIndex > fingerprintIndex, "request construction must follow the authority guard");
  assert.ok(mutationIndex > requestIndex, "mutation must follow every pre-submit guard");
  assert.equal(
    matchCount(sources.controller, /dependencies\.mutate\s*\(/g),
    1,
    "stream controller must contain exactly one guarded mutation path",
  );
  assert.match(
    sources.controller,
    /snapshot:\s*dependencies\.getPermissions\(\)/,
    "pre-submit evaluation must consume the current permission provider",
  );
}

export function mutateStreamsStartReadinessHandlerGuard(
  sources: StreamsStartReadinessGuardSources,
  mutation: StreamsStartReadinessGuardMutation,
): StreamsStartReadinessGuardSources {
  if (mutation === "remove-current-permission-snapshot") {
    return { ...sources, view: replaceExactly(sources.view, permissionSnapshotWiring, "getPermissions: () => ({ kind: \"unavailable\" })") };
  }
  if (mutation === "use-streams-update-authority") {
    return {
      ...sources,
      descriptors: replaceExactly(
        sources.descriptors,
        'template("STR-08", "guarded", "streams.start"',
        'template("STR-08", "guarded", "streams.update"',
      ),
    };
  }
  if (mutation === "remove-pre-submit-evaluation") {
    return { ...sources, controller: replaceExactly(sources.controller, preSubmitEvaluation, "const reason = undefined;") };
  }
  if (mutation === "move-mutation-before-guard") {
    let controller = replaceExactly(
      sources.controller,
      preSubmitEvaluation,
      `const prematureValue = await dependencies.mutate({ id: opened.intent.id } as never);\n      ${preSubmitEvaluation}`,
    );
    controller = replaceExactly(controller, guardedMutation, "const value = prematureValue;");
    return { ...sources, controller };
  }
  return {
    ...sources,
    controller: `${sources.controller}\nfunction alternateUnguardedPath(dependencies: { mutate: (value: unknown) => unknown }) { return dependencies.mutate({}); }\n`,
  };
}

function matchCount(source: string, expression: RegExp) {
  return [...source.matchAll(expression)].length;
}

function replaceExactly(source: string, target: string, replacement: string) {
  assert.equal(source.split(target).length - 1, 1, `mutation fixture must match exactly once: ${target}`);
  return source.replace(target, replacement);
}
