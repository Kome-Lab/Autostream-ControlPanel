import assert from "node:assert/strict";
import ts from "typescript";
export function copyBindings(source:string) {
  const file=ts.createSourceFile("current.tsx",source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
  const keys:string[]=[];const hooks:string[]=[];const defaults:string[]=[];const imports:string[]=[];
  function visit(node:ts.Node) {
    if(ts.isCallExpression(node)&&node.expression.getText(file)==="uiText"&&node.arguments[0]&&ts.isStringLiteral(node.arguments[0]))keys.push(node.arguments[0].text);
    if(ts.isVariableDeclaration(node)&&node.initializer&&ts.isCallExpression(node.initializer)&&node.initializer.expression.getText(file)==="useUICopy")hooks.push(node.name.getText(file));
    if(ts.isParameter(node)&&node.name.getText(file)==="uiText"&&node.initializer)defaults.push(node.initializer.getText(file));
    if(ts.isImportDeclaration(node)&&ts.isStringLiteral(node.moduleSpecifier)&&node.moduleSpecifier.text.includes("/i18n/ui-v2/"))imports.push(node.moduleSpecifier.text);
    ts.forEachChild(node,visit);
  }
  visit(file);return {keys:keys.sort(),hooks:hooks.sort(),defaults:defaults.sort(),imports:imports.sort()};
}
export function assertCopyBinding(source:string,expected:ReturnType<typeof copyBindings>) {
 const actual=copyBindings(source);
 assert.deepEqual(actual.imports,expected.imports,"current copy import disconnected");
 assert.deepEqual(actual.hooks,expected.hooks,"current component copy owner disconnected");
 assert.deepEqual(actual.defaults,expected.defaults,"current presenter default disconnected");
 assert.deepEqual(actual.keys,expected.keys,"current displayed key missing or duplicated");
}
