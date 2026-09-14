import assert from "node:assert/strict";
import ts from "typescript";

// Execute the real owner callback with only its outer collaborators substituted.
// This avoids an invented save-outcome model and requires no browser or production API.
export function actualCallback(source: string, name: string, bindings: Record<string, unknown>) {
  const file = ts.createSourceFile("owner.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  let callback: ts.Expression | undefined;
  function visit(node: ts.Node) {
    if (ts.isVariableDeclaration(node) && node.name.getText(file) === name && node.initializer) {
      callback = ts.isCallExpression(node.initializer) && node.initializer.expression.getText(file) === "useCallback"
        ? node.initializer.arguments[0] : node.initializer;
    }
    ts.forEachChild(node, visit);
  }
  visit(file); assert.ok(callback, "actual callback must exist: " + name);
  const compiled = ts.transpileModule("const callback = " + callback.getText(file) + ";", {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
  }).outputText;
  return new Function(...Object.keys(bindings), compiled + "\nreturn callback;")(...Object.values(bindings));
}

export function actualJSXCallback(source: string, tag: string, prop: string, bindings: Record<string, unknown>) {
  const file=ts.createSourceFile("owner.tsx",source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
  const matches:ts.Expression[]=[];
  function visit(node:ts.Node) {
    if((ts.isJsxSelfClosingElement(node)||ts.isJsxOpeningElement(node))&&node.tagName.getText(file)===tag) {
      for(const attribute of node.attributes.properties)if(ts.isJsxAttribute(attribute)&&attribute.name.getText(file)===prop&&attribute.initializer&&ts.isJsxExpression(attribute.initializer)&&attribute.initializer.expression)matches.push(attribute.initializer.expression);
    }
    ts.forEachChild(node,visit);
  }
  visit(file);assert.equal(matches.length,1,"actual unique JSX callback: "+tag+"."+prop);
  return actualCallback("const selectedCallback="+matches[0].getText(file),"selectedCallback",bindings);
}
export function actualFunction(source: string, name: string, bindings: Record<string, unknown>) {
  const file=ts.createSourceFile("owner.tsx",source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
  const declaration=file.statements.find(node=>ts.isFunctionDeclaration(node)&&node.name?.text===name);
  assert.ok(declaration,"actual function: "+name);
  return actualCallback("const selectedCallback="+declaration.getText(file),"selectedCallback",bindings);
}

export function actualEffect(source:string,needle:string,bindings:Record<string,unknown>) {
 const file=ts.createSourceFile("owner.tsx",source,ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
 const matches:ts.Expression[]=[];
 function visit(node:ts.Node){if(ts.isCallExpression(node)&&node.expression.getText(file)==="useEffect"&&node.arguments[0]?.getText(file).includes(needle))matches.push(node.arguments[0]);ts.forEachChild(node,visit);}
 visit(file);assert.equal(matches.length,1,"unique actual effect");
 return actualCallback("const selectedCallback="+matches[0].getText(file),"selectedCallback",bindings);
}
