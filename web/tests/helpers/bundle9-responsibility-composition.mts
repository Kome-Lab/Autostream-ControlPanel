import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import ts from "typescript";

const factories = new Set([
  "createApplicationAuthorityReaders", "createApplicationActionRenderers",
  "usePullOwnershipMutations", "useNodeRegistrationMutations", "createRegisteredNodeColumns",
]);
const sections = new Set([
  "UpdaterRuntimeSettingsSection", "UpdaterBootstrapHostsSection", "UpdaterTargetsSettingsSection",
  "UpdaterReleaseTokenSection", "NodeConfigurationCard", "NodeEditDialog",
]);
type Binding = { node: ts.Expression; source: ts.SourceFile };
type Bindings = Map<string, Binding>;

/** Expand only the named extraction boundaries, using their real imports and argument bindings.
 * The resulting declaration still has to match the unchanged before token digest.
 * No after digest or copy of a before function is accepted as a substitute.
 */
export function composeResponsibilitySource(
  file: string,
  readSource: (file: string) => string = (name) => readFileSync(name, "utf8"),
) {
  const loaded = new Map<string, ts.SourceFile>();
  function load(name: string) {
    let source = loaded.get(name);
    if (!source) {
      source = ts.createSourceFile(name, readSource(name), ts.ScriptTarget.Latest, true);
      assert.equal(source.parseDiagnostics.length, 0, `${name}: source parse`);
      loaded.set(name, source);
    }
    return source;
  }
  function declaration(source: ts.SourceFile, name: string) {
    const imports = source.statements.filter(ts.isImportDeclaration);
    const owner = imports.flatMap((entry) => {
      if (!ts.isStringLiteral(entry.moduleSpecifier) || !entry.moduleSpecifier.text.startsWith("./")) return [];
      const bindings = entry.importClause?.namedBindings;
      if (!bindings || !ts.isNamedImports(bindings)) return [];
      return bindings.elements.filter((item) => item.name.text === name)
        .map((item) => ({ specifier: entry.moduleSpecifier.text, exported: item.propertyName?.text || item.name.text }));
    });
    assert.equal(owner.length, 1, `${name}: exactly one actual import`);
    const base = resolve(dirname(source.fileName), owner[0].specifier);
    let imported: ts.SourceFile | undefined;
    for (const candidate of [base, `${base}.ts`, `${base}.tsx`]) {
      try { imported = load(candidate); break; } catch (error) {
        if (!(error instanceof Error) || !("code" in error) || error.code !== "ENOENT") throw error;
      }
    }
    assert.ok(imported, `${name}: imported source exists`);
    const matches = imported.statements.filter((node): node is ts.FunctionDeclaration => ts.isFunctionDeclaration(node) && node.name?.text === owner[0].exported);
    assert.equal(matches.length, 1, `${name}: one implementation`);
    assert.ok(matches[0].body);
    return { source: imported, fn: matches[0] };
  }
  function propertyBindings(argument: ts.ObjectLiteralExpression, source: ts.SourceFile) {
    const result = new Map<string, Binding>();
    for (const property of argument.properties) {
      assert.ok(ts.isPropertyAssignment(property) || ts.isShorthandPropertyAssignment(property), "closed extraction arguments");
      assert.ok(ts.isIdentifier(property.name));
      assert.equal(result.has(property.name.text), false, "duplicate extraction binding");
      result.set(property.name.text, { node: ts.isShorthandPropertyAssignment(property) ? property.name : property.initializer, source });
    }
    return result;
  }
  function bindPattern(pattern: ts.BindingName, values: Bindings, source: ts.SourceFile): Bindings {
    assert.ok(ts.isObjectBindingPattern(pattern), "extraction uses a closed object binding");
    const result = new Map<string, Binding>();
    const keys = pattern.elements.map((element) => element.propertyName?.getText(source) || element.name.getText(source));
    assert.deepEqual([...values.keys()].sort(), [...keys].sort(), "all extracted inputs remain connected");
    for (const element of pattern.elements) {
      assert.equal(element.dotDotDotToken, undefined, "no broad extraction context");
      assert.equal(element.initializer, undefined, "no default changes at extraction boundary");
      const key = element.propertyName?.getText(source) || element.name.getText(source);
      const value = values.get(key)!;
      if (ts.isIdentifier(element.name)) result.set(element.name.text, value);
      else {
        assert.ok(ts.isObjectLiteralExpression(value.node));
        for (const [name, nested] of bindPattern(element.name, propertyBindings(value.node, value.source), source)) result.set(name, nested);
      }
    }
    return result;
  }
  function replaceChildren(node: ts.Node, source: ts.SourceFile, bindings: Bindings) {
    let text = node.getText(source);
    const edits: { start: number; end: number; text: string }[] = [];
    ts.forEachChild(node, (child) => {
      const next = render(child, source, bindings);
      if (next !== child.getText(source)) edits.push({ start: child.getStart(source) - node.getStart(source), end: child.end - node.getStart(source), text: next });
    });
    for (const edit of edits.sort((a, b) => b.start - a.start)) text = text.slice(0, edit.start) + edit.text + text.slice(edit.end);
    return text;
  }
  function expression(node: ts.Expression) {
    while (ts.isParenthesizedExpression(node)) node = node.expression;
    return node;
  }
  function render(node: ts.Node, source: ts.SourceFile, bindings: Bindings): string {
    if (ts.isVariableStatement(node) && node.declarationList.declarations.length === 1) {
      const item = node.declarationList.declarations[0];
      if (item.initializer && ts.isCallExpression(item.initializer) && ts.isIdentifier(item.initializer.expression) && factories.has(item.initializer.expression.text)) {
        const call = item.initializer, { source: owner, fn } = declaration(source, call.expression.getText(source));
        assert.equal(call.arguments.length, 1);
        assert.ok(ts.isObjectLiteralExpression(call.arguments[0]));
        const parameters = bindPattern(fn.parameters[0].name, propertyBindings(call.arguments[0], source), owner);
        const statements = fn.body!.statements, last = statements.at(-1)!;
        assert.ok(ts.isReturnStatement(last) && last.expression, "factory retains its result");
        const returned = ts.isObjectLiteralExpression(last.expression) ? last.expression.properties.map((property) => property.name?.getText(owner)).sort() : [last.expression.getText(owner)];
        const assigned = ts.isObjectBindingPattern(item.name) ? item.name.elements.map((element) => element.name.getText(source)).sort() : [item.name.getText(source)];
        assert.deepEqual(returned, assigned, "extracted result identity");
        return statements.slice(0, -1).map((statement, index) => (
          (index === 0 ? "" : owner.text.slice(statements[index - 1].end, statement.getStart(owner)))
          + render(statement, owner, parameters)
        )).join("");
      }
      if (item.name.getText(source) === "refreshPortAuthority" && item.initializer && ts.isArrowFunction(item.initializer)) {
        const call = item.initializer.body;
        assert.ok(ts.isCallExpression(call) && call.expression.getText(source) === "refreshApplicationPortAuthority");
        assert.equal(call.arguments[0].getText(source), item.initializer.parameters[0].name.getText(source));
        const { source: owner, fn } = declaration(source, "refreshApplicationPortAuthority");
        assert.ok(ts.isObjectLiteralExpression(call.arguments[1]));
        const parameters = bindPattern(fn.parameters[1].name, propertyBindings(call.arguments[1], source), owner);
        const body = render(fn.body!, owner, parameters).replace(/\n\s*}$/, "\n  }");
        return `const refreshPortAuthority = async (${fn.parameters[0].getText(owner)}): ${fn.type!.getText(owner)} => ${body};`;
      }
    }
    if (ts.isJsxSelfClosingElement(node) && sections.has(node.tagName.getText(source))) {
      const { source: owner, fn } = declaration(source, node.tagName.getText(source));
      const values = new Map<string, Binding>();
      for (const attribute of node.attributes.properties) {
        assert.ok(ts.isJsxAttribute(attribute) && attribute.initializer && ts.isJsxExpression(attribute.initializer) && attribute.initializer.expression);
        values.set(attribute.name.getText(source), { node: attribute.initializer.expression, source });
      }
      const parameters = bindPattern(fn.parameters[0].name, values, owner);
      assert.equal(fn.body!.statements.length, 1, "display section has no hidden state or effects");
      const returned = fn.body!.statements[0];
      assert.ok(ts.isReturnStatement(returned) && returned.expression);
      return render(expression(returned.expression), owner, parameters);
    }
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && bindings.has(node.expression.text)) {
      const binding = bindings.get(node.expression.text)!;
      if (ts.isArrowFunction(binding.node)) {
        assert.equal(binding.node.modifiers?.length || 0, 0, "callback timing is unchanged");
        assert.equal(binding.node.parameters.length, node.arguments.length);
        const parameters = new Map<string, Binding>();
        binding.node.parameters.forEach((parameter, index) => {
          assert.ok(ts.isIdentifier(parameter.name));
          parameters.set(parameter.name.text, { node: node.arguments[index], source });
        });
        return render(binding.node.body, binding.source, parameters);
      }
    }
    if (ts.isIdentifier(node) && bindings.has(node.text)) {
      const parent = node.parent;
      const namedKey = (ts.isPropertyAccessExpression(parent) && parent.name === node)
        || (ts.isPropertyAssignment(parent) && parent.name === node)
        || (ts.isJsxAttribute(parent) && parent.name === node)
        || ((ts.isVariableDeclaration(parent) || ts.isParameter(parent)) && parent.name === node);
      if (!namedKey) return bindings.get(node.text)!.node.getText(bindings.get(node.text)!.source);
    }
    return replaceChildren(node, source, bindings);
  }
  const source = load(file);
  return render(source, source, new Map());
}
