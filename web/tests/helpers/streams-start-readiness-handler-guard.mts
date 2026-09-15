import assert from "node:assert/strict";
import ts from "typescript";

export type StreamsStartReadinessGuardSources = Readonly<{
  view: string;
  cells: string;
  detailDialog: string;
  details: string;
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
  assertReadinessOwnerBindings(sources);
  assert.equal(
    matchCount(sources.view, /getPermissions:\s*\(\)\s*=>\s*streamPermissionSnapshot\(queryClient\)/g),
    1,
    "Streams controller wiring must read the current permission snapshot",
  );
  for (const source of [sources.view, sources.cells, sources.detailDialog, sources.details]) assert.doesNotMatch(
    source,
    /\b(?:apiGet|apiPost|apiPut|apiDelete|fetch)\s*\([^\n]*start-readiness/,
    "Streams owners must not add an alternate start-readiness transport path",
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

function assertReadinessOwnerBindings(sources: StreamsStartReadinessGuardSources) {
  const files = Object.fromEntries((["view", "cells", "detailDialog", "details"] as const).map(key =>
    [key, ts.createSourceFile(key + ".tsx", sources[key], ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)])) as Record<"view" | "cells" | "detailDialog" | "details", ts.SourceFile>;
  const compact = (node: ts.Node) => node.getText().replace(/\s+/g, "");
  function nodes<T extends ts.Node>(root: ts.Node, guard: (node: ts.Node) => node is T): T[] {
    const found: T[] = []; function visit(node: ts.Node) { if (guard(node)) found.push(node); ts.forEachChild(node, visit); } visit(root); return found;
  }
  function one<T>(values: T[], label: string) { assert.equal(values.length, 1, label + " must have exactly one connected declaration"); return values[0]; }
  function owner(file: ts.SourceFile, name: string) { return one(file.statements.filter(ts.isFunctionDeclaration).filter(node => node.name?.text === name), name); }
  function variable(root: ts.Node, name: string) { const node = one(nodes(root, ts.isVariableDeclaration).filter(node => ts.isIdentifier(node.name) && node.name.text === name), name); assert.ok(node.initializer, name + " initializer missing"); return node.initializer; }
  function element(root: ts.Node, tag: string) { return nodes(root, (node): node is ts.JsxOpeningElement | ts.JsxSelfClosingElement => ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)).filter(node => compact(node.tagName) === tag); }
  function attribute(node: ts.JsxOpeningElement | ts.JsxSelfClosingElement, name: string) {
    const attr = one(node.attributes.properties.filter(ts.isJsxAttribute).filter(attr => attr.name.getText() === name), name);
    assert.ok(attr.initializer && ts.isJsxExpression(attr.initializer) && attr.initializer.expression, name + " expression missing"); return compact(attr.initializer.expression);
  }
  function imported(file: ts.SourceFile, name: string, path: string) {
    assert.equal(file.statements.filter(ts.isImportDeclaration).filter(node => ts.isStringLiteral(node.moduleSpecifier) && node.moduleSpecifier.text === path && node.importClause?.namedBindings && ts.isNamedImports(node.importClause.namedBindings) && node.importClause.namedBindings.elements.some(item => item.name.text === name && !item.propertyName)).length, 1, name + " must use its actual imported owner");
  }
  imported(files.view, "StreamTableContext", "./stream-table-cells");
  imported(files.view, "streamTableColumns", "./stream-table-cells");
  imported(files.view, "StreamDetailsDialog", "@/features/streams/stream-details-dialog");
  imported(files.detailDialog, "StreamDetailOperations", "./stream-detail-operations");
  for (const file of [files.cells, files.details]) imported(file, "StreamActionControl", "./stream-action-control");
  const view = owner(files.view, "StreamsView"), cells = owner(files.cells, "StreamActionsCell"), details = owner(files.details, "StreamDetailOperations");
  const controller = variable(view, "actionController");
  assert.ok(ts.isCallExpression(controller) && compact(controller.expression) === "useMemo", "existing controller memo owner missing");
  assert.equal(nodes(view, ts.isCallExpression).filter(node => compact(node.expression) === "createStreamActionController").length, 1, "view must create one shared action controller");
  assert.equal(nodes(controller, ts.isCallExpression).filter(node => compact(node.expression) === "createStreamActionController").length, 1, "controller declaration must own the actual creation");
  const presentation = variable(view, "tablePresentation");
  assert.ok(ts.isObjectLiteralExpression(presentation) && presentation.properties.some(node => ts.isShorthandPropertyAssignment(node) && node.name.text === "actionController"), "table presentation must pass the same controller");
  const provider = one(element(view, "StreamTableContext.Provider"), "table provider");
  assert.equal(attribute(provider, "value"), "tablePresentation", "table provider must consume the current presentation");
  const table = one(element(provider.parent, "DataTable"), "provided table");
  assert.equal(attribute(table, "columns"), "columns", "provided table must consume real columns");
  assert.match(compact(variable(view, "columns")), /^streamTableColumns\(/, "view must bind the real column factory");
  const columns = owner(files.cells, "streamTableColumns");
  assert.equal(nodes(columns, ts.isPropertyAssignment).filter(node => node.name.getText() === "cell" && compact(node.initializer) === "StreamActionsCell").length, 1, "action cell must be registered in the actual column factory");
  const context = owner(files.cells, "useStreamTablePresentation");
  assert.equal(compact(variable(context, "value")), "useContext(StreamTableContext)", "row context must read the actual provider");
  assert.ok(nodes(cells, ts.isVariableDeclaration).some(node => ts.isObjectBindingPattern(node.name) && node.name.elements.some(item => item.name.getText() === "actionController") && node.initializer && compact(node.initializer) === "useStreamTablePresentation()"), "row must consume the same controller from its context");
  const readiness = element(cells, "StreamActionControl").filter(node => /id:"STR-08"/.test(attribute(node, "intent")));
  const rowControl = one(readiness, "row STR-08 control");
  assert.equal(attribute(rowControl, "controller"), "actionController", "row STR-08 must use the shared controller");
  assert.equal(attribute(rowControl, "intent"), '{id:"STR-08",stream:row.original}', "row STR-08 must use its real record");
  assert.equal(attribute(one(element(view, "StreamDetailsDialog"), "detail dialog"), "actionController"), "actionController", "detail dialog must receive the same controller");
  assert.equal(attribute(one(element(owner(files.detailDialog, "StreamDetailsDialog"), "StreamDetailOperations"), "detail operations"), "controller"), "actionController", "detail operations must receive the same controller");
  const controls = variable(details, "controls");
  assert.equal(nodes(controls, ts.isPropertyAssignment).filter(node => node.name.getText() === "id" && ts.isStringLiteral(node.initializer) && node.initializer.text === "STR-08").length, 1, "detail must declare exactly one STR-08 action");
  const mapping = one(nodes(details, ts.isCallExpression).filter(node => compact(node.expression) === "controls.map"), "detail action mapping");
  const detailControl = one(element(mapping, "StreamActionControl"), "mapped detail control");
  assert.equal(attribute(detailControl, "controller"), "controller", "mapped detail control must use the shared controller");
  assert.equal(attribute(detailControl, "intent"), "intent", "mapped detail control must use its action intent");
  assert.equal(compact(variable(mapping, "intent")), "{id,stream}", "detail intent must retain the mapped action and actual stream");
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
