import assert from "node:assert/strict";
import ts from "typescript";

// Resolve the moved display boolean through the real JSX caller. Original
// residual lines retain their expression, text, order, count and digest.
export function projectNodeResidualBindings(sources: ReadonlyMap<string, string>) {
  const entry = "web/src/features/nodes/node-registration-view.tsx";
  const owner = "web/src/features/nodes/node-edit-dialog.tsx";
  const parent = ts.createSourceFile(entry, sources.get(entry)!, ts.ScriptTarget.Latest, true);
  const child = ts.createSourceFile(owner, sources.get(owner)!, ts.ScriptTarget.Latest, true);
  const calls: ts.JsxSelfClosingElement[] = [];
  function find(node: ts.Node) {
    if (ts.isJsxSelfClosingElement(node) && node.tagName.getText(parent) === "NodeEditDialog") calls.push(node);
    ts.forEachChild(node, find);
  }
  find(parent);
  assert.equal(calls.length, 1, "one Node edit display caller");
  const props = calls[0].attributes.properties.filter((property): property is ts.JsxAttribute => ts.isJsxAttribute(property) && property.name.getText(parent) === "updatePending");
  assert.equal(props.length, 1, "one Node updatePending binding");
  const value = props[0].initializer;
  assert.ok(value && ts.isJsxExpression(value) && value.expression, "Node pending binding expression");
  const expression = value.expression.getText(parent);
  assert.equal(expression, "updateNode.isPending", "original Node pending authority");
  const functions = child.statements.filter((node): node is ts.FunctionDeclaration => ts.isFunctionDeclaration(node) && node.name?.text === "NodeEditDialog");
  assert.equal(functions.length, 1, "one Node edit display owner");
  const fn = functions[0];
  assert.ok(fn.body && ts.isObjectBindingPattern(fn.parameters[0].name));
  assert.equal(fn.parameters[0].name.elements.filter((element) => element.name.getText(child) === "updatePending").length, 1, "Node pending input remains connected");
  const edits: { start: number; end: number }[] = [];
  function visit(node: ts.Node) {
    if ((ts.isVariableDeclaration(node) || ts.isParameter(node)) && node.name.getText(child) === "updatePending") assert.fail("shadowed Node pending binding");
    if (ts.isIdentifier(node) && node.text === "updatePending") edits.push({ start: node.getStart(child), end: node.end });
    ts.forEachChild(node, visit);
  }
  visit(fn.body!);
  assert.equal(edits.length, 2, "Node pending display and disabled bindings");
  let projected = child.text;
  for (const edit of edits.sort((a, b) => b.start - a.start)) projected = projected.slice(0, edit.start) + expression + projected.slice(edit.end);
  return new Map([...sources].map(([path, source]) => [path, path === owner ? projected : source]));
}
