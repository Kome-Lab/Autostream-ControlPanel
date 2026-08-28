import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import ts from "typescript";

export function assertWorkerFoundationBoundaries(webRoot: string) {
  const descriptorPath = join(webRoot, "src", "features", "workers", "workers-action-descriptors.ts");
  const controllerPath = join(webRoot, "src", "features", "workers", "workers-action-controller.ts");
  const configurationDescriptorPath = join(webRoot, "src", "features", "workers", "workers-configuration-descriptor.ts");
  const configurationControllerPath = join(webRoot, "src", "features", "workers", "workers-configuration-controller.ts");
  const normalizerPath = join(webRoot, "src", "features", "workers", "workers-wire-normalizer.ts");
  const viewPath = join(webRoot, "src", "features", "workers", "workers-view.tsx");
  const browserTestPath = join(webRoot, "tests", "ui-foundation-browser.test.mts");
  assert.equal(existsSync(descriptorPath), true, "Worker action descriptor is missing");
  assert.equal(existsSync(controllerPath), true, "Worker restart controller is missing");
  assert.equal(existsSync(configurationDescriptorPath), true, "Worker Configuration descriptor is missing");
  assert.equal(existsSync(configurationControllerPath), true, "Worker Configuration controller is missing");
  assert.equal(existsSync(normalizerPath), true, "Worker wire normalizer is missing");

  const descriptor = parse(descriptorPath);
  const descriptorImports = imports(descriptor);
  assert.equal(descriptorImports.some((value) => /api|@tanstack|router|navigation|queries/.test(value)), false, "descriptor owns execution infrastructure");
  assert.equal(endpointLiterals(descriptor).length, 0, "descriptor embeds an endpoint or method");
  assert.deepEqual(moduleMutableDeclarations(descriptor), [], "descriptor has module-global mutable state");

  const controller = parse(controllerPath);
  const controllerSource = controller.getFullText();
  assert.equal(/foundation\/secrets|one-time-secret|sessionStorage|localStorage|analytics/.test(controllerSource), false, "controller imports a forbidden capability");
  assert.deepEqual(moduleMutableDeclarations(controller), [], "controller has a module-global lock or mutable state");
  const queryKeys = stringArrayLiterals(controller)
    .map((parts) => JSON.stringify(parts))
    .filter((value) => value === '["auth","me"]' || value === '["workers"]');
  assert.equal(queryKeys.includes('["auth","me"]'), true, "controller must read the existing auth query key");
  assert.equal(queryKeys.includes('["workers"]'), true, "controller must use the existing workers query key");
  assert.equal(controllerSource.includes("retry"), false, "restart controller must not own an automatic retry loop");

  const configurationDescriptor = parse(configurationDescriptorPath);
  assert.equal(imports(configurationDescriptor).some((value) => /api|@tanstack|router|navigation|queries/.test(value)), false, "Configuration descriptor owns execution infrastructure");
  assert.equal(endpointLiterals(configurationDescriptor).length, 0, "Configuration descriptor embeds an endpoint or method");
  assert.deepEqual(moduleMutableDeclarations(configurationDescriptor), [], "Configuration descriptor has module-global mutable state");

  const configurationController = parse(configurationControllerPath);
  const configurationSource = configurationController.getFullText();
  assert.equal(/foundation\/secrets|one-time-secret|sessionStorage|localStorage|analytics/.test(configurationSource), false, "Configuration controller imports a forbidden capability");
  assert.equal(configurationSource.includes('["auth", "me"]'), true, "Configuration controller must read the existing auth key");
  assert.equal(/queryKey\s*:/.test(configurationSource), false, "Configuration controller must not create a query key");
  assert.equal(/\.message\b/.test(configurationSource), false, "Configuration controller reads a raw error message");
  assert.deepEqual(moduleMutableDeclarations(configurationController), [], "Configuration controller has module-global mutable state");

  const normalizer = parse(normalizerPath);
  const normalizerSource = normalizer.getFullText();
  assert.equal(imports(normalizer).some((value) => /api|@tanstack|router|navigation|queries|foundation\//.test(value)), false, "Worker normalizer imports execution infrastructure");
  assert.deepEqual(moduleMutableDeclarations(normalizer), [], "Worker normalizer has module-global mutable state");
  assert.equal(/\bas\s+(?:any|never)\b|unknown\s+as\s+|@ts-ignore/.test(normalizerSource), false, "Worker normalizer contains a forbidden cast or suppression");
  assert.equal(controllerSource.includes("workers-wire-normalizer"), true, "restart controller does not use the canonical Worker normalizer");
  assert.equal(configurationSource.includes("workers-wire-normalizer"), true, "Configuration controller does not use the canonical Worker normalizer");

  const browserTest = parse(browserTestPath);
  for (const properties of workerPilotFixtureProperties(browserTest)) {
    assert.equal(properties.includes("service_id"), true, "primary browser Worker fixture omits service_id");
    assert.equal(properties.includes("id"), false, "primary browser Worker fixture hides the server wire with an artificial id");
  }
  const configurationNodeProperties = workerConfigurationNodeProperties(browserTest);
  assert.equal(configurationNodeProperties.includes("service_id"), true, "primary browser Configuration node omits service_id");
  assert.equal(configurationNodeProperties.includes("id"), false, "primary browser Configuration node adds an artificial id");

  const view = parse(viewPath);
  const viewSource = view.getFullText();
  assert.equal(viewSource.includes("DangerConfirm"), false, "DangerConfirm still owns Worker restart");
  assert.equal(viewSource.includes("HighRiskConfirmation"), true, "HighRiskConfirmation does not own Worker restart");
  assert.equal(/foundation\/secrets|one-time-secret/.test(viewSource), false, "Workers view imports B06 secret Foundation");
  assert.equal(viewSource.includes("ActionAvailabilityBoundary"), true, "Configuration entry point lacks the availability boundary");
  assert.equal(viewSource.includes("worker-configuration-reason-"), true, "Configuration denial lacks a described reason");
  assert.equal(viewSource.includes("RemoteStateBoundary"), true, "Configuration does not use the canonical remote-state boundary");
  assert.equal(/APIError|\.message\b/.test(viewSource), false, "Workers view renders a native error surface");
  assertWorkerRestartTriggerComposition(view);
  return Object.freeze({ descriptorFiles: 2, controllerFiles: 2, normalizerFiles: 1 });
}

function parse(path: string) {
  return ts.createSourceFile(path, readFileSync(path, "utf8"), ts.ScriptTarget.Latest, true, path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
}

export function workerRestartTriggerCompositionIssues(
  sourceText: string,
  fileName = "workers-view.tsx",
) {
  return inspectWorkerRestartTriggerComposition(
    ts.createSourceFile(fileName, sourceText, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX),
  );
}

function assertWorkerRestartTriggerComposition(sourceFile: ts.SourceFile) {
  const issues = inspectWorkerRestartTriggerComposition(sourceFile);
  assert.deepEqual(
    issues,
    [],
    `Worker restart trigger composition is invalid: ${issues.join(", ")}`,
  );
}

function inspectWorkerRestartTriggerComposition(sourceFile: ts.SourceFile) {
  const issues = new Set<string>();
  const confirmations: ts.JsxSelfClosingElement[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isJsxSelfClosingElement(node) && node.tagName.getText(sourceFile) === "HighRiskConfirmation") {
      confirmations.push(node);
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);

  if (confirmations.length !== 1) {
    issues.add("high-risk-confirmation-count");
    return [...issues];
  }
  const confirmation = confirmations[0];
  if (!hasJsxAncestor(confirmation, sourceFile, "ActionAvailabilityBoundary")) {
    issues.add("availability-boundary-not-outer");
  }

  const trigger = confirmation.attributes.properties.find((property) =>
    ts.isJsxAttribute(property) && property.name.getText(sourceFile) === "trigger");
  if (!trigger
    || !ts.isJsxAttribute(trigger)
    || !trigger.initializer
    || !ts.isJsxExpression(trigger.initializer)
    || !trigger.initializer.expression
    || !ts.isArrowFunction(trigger.initializer.expression)) {
    issues.add("trigger-callback");
    return [...issues];
  }

  let triggerBody: ts.ConciseBody = trigger.initializer.expression.body;
  while (ts.isParenthesizedExpression(triggerBody)) triggerBody = triggerBody.expression;
  if (!ts.isJsxElement(triggerBody) && !ts.isJsxSelfClosingElement(triggerBody)) {
    issues.add("immediate-trigger-button");
    return [...issues];
  }
  if (jsxElementName(triggerBody, sourceFile) !== "Button") {
    issues.add("immediate-trigger-button");
    return [...issues];
  }

  const triggerAttributes = jsxAttributes(triggerBody);
  const refAttribute = triggerAttributes.find((property) =>
    ts.isJsxAttribute(property) && property.name.getText(sourceFile) === "ref");
  if (!refAttribute
    || !ts.isJsxAttribute(refAttribute)
    || !refAttribute.initializer
    || !ts.isJsxExpression(refAttribute.initializer)
    || !refAttribute.initializer.expression
    || !ts.isIdentifier(refAttribute.initializer.expression)
    || refAttribute.initializer.expression.text !== "restartTriggerRef") {
    issues.add("restart-trigger-ref");
  }

  const onClickAttribute = triggerAttributes.find((property) =>
    ts.isJsxAttribute(property) && property.name.getText(sourceFile) === "onClick");
  if (onClickAttribute && ts.isJsxAttribute(onClickAttribute)) {
    const onClickExpression = jsxAttributeExpression(onClickAttribute);
    const onOpenAttribute = confirmation.attributes.properties.find((property) =>
      ts.isJsxAttribute(property) && property.name.getText(sourceFile) === "onOpenIntent");
    const onOpenExpression = onOpenAttribute && ts.isJsxAttribute(onOpenAttribute)
      ? jsxAttributeExpression(onOpenAttribute)
      : undefined;
    if (onClickExpression && isManualDialogOpenPath(onClickExpression, onOpenExpression, sourceFile)) {
      issues.add("manual-open-onclick");
    }
  }

  if (hasInteractiveTriggerDescendant(triggerBody, sourceFile)) {
    issues.add("interactive-trigger-descendant");
  }
  return [...issues];
}

function jsxAttributes(node: ts.JsxElement | ts.JsxSelfClosingElement) {
  return ts.isJsxElement(node)
    ? node.openingElement.attributes.properties
    : node.attributes.properties;
}

function jsxAttributeExpression(attribute: ts.JsxAttribute) {
  return attribute.initializer
    && ts.isJsxExpression(attribute.initializer)
    && attribute.initializer.expression
    ? attribute.initializer.expression
    : undefined;
}

function isManualDialogOpenPath(
  onClickExpression: ts.Expression,
  onOpenExpression: ts.Expression | undefined,
  sourceFile: ts.SourceFile,
) {
  const openTargets = onOpenExpression
    ? collectActivationTargets(onOpenExpression, sourceFile)
    : new Set<string>();
  const clickTargets = collectActivationTargets(onClickExpression, sourceFile);
  for (const target of clickTargets) {
    const shortTarget = target.split(".").at(-1) || target;
    if (openTargets.has(target) || openTargets.has(shortTarget)) return true;
    if (/^(?:set.*(?:open|dialog)|open.*(?:dialog|restart)|.*(?:dialog|restart).*open)$/i.test(shortTarget)) return true;
    if (target.endsWith(".open")) return true;
  }
  return false;
}

function collectActivationTargets(expression: ts.Expression, sourceFile: ts.SourceFile) {
  const targets = new Set<string>();
  const resolvedHelpers = new Set<string>();
  const resolveHelper = (name: string) => {
    if (resolvedHelpers.has(name)) return;
    resolvedHelpers.add(name);
    const declarations: ts.Node[] = [];
    const find = (node: ts.Node) => {
      if (ts.isVariableDeclaration(node)
        && ts.isIdentifier(node.name)
        && node.name.text === name
        && node.initializer) declarations.push(node.initializer);
      if (ts.isFunctionDeclaration(node)
        && node.name?.text === name
        && node.body) declarations.push(node.body);
      ts.forEachChild(node, find);
    };
    find(sourceFile);
    for (const declaration of declarations) visit(declaration);
  };
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node)) {
      const target = node.expression.getText(sourceFile);
      targets.add(target);
      targets.add(target.split(".").at(-1) || target);
      if (ts.isIdentifier(node.expression)) resolveHelper(node.expression.text);
    }
    ts.forEachChild(node, visit);
  };
  if (ts.isIdentifier(expression)) {
    targets.add(expression.text);
    resolveHelper(expression.text);
  }
  visit(expression);
  return targets;
}

function hasInteractiveTriggerDescendant(
  triggerButton: ts.JsxElement | ts.JsxSelfClosingElement,
  sourceFile: ts.SourceFile,
) {
  let interactive = false;
  const safeCustomChildren = new Set(["RotateCw"]);
  const interactiveElements = new Set([
    "Button",
    "Link",
    "a",
    "button",
    "details",
    "input",
    "select",
    "summary",
    "textarea",
  ]);
  const visit = (node: ts.Node) => {
    if (node !== triggerButton && (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node))) {
      const name = jsxElementName(node, sourceFile);
      const attributes = jsxAttributes(node);
      const role = attributes.find((property) =>
        ts.isJsxAttribute(property) && property.name.getText(sourceFile) === "role");
      const hasInteractionAttribute = attributes.some((property) =>
        ts.isJsxAttribute(property)
          && ["onClick", "tabIndex"].includes(property.name.getText(sourceFile)));
      if (interactiveElements.has(name)
        || role !== undefined
        || hasInteractionAttribute
        || (/^[A-Z]/.test(name) && !safeCustomChildren.has(name))) {
        interactive = true;
      }
    }
    ts.forEachChild(node, visit);
  };
  ts.forEachChild(triggerButton, visit);
  return interactive;
}

function hasJsxAncestor(node: ts.Node, sourceFile: ts.SourceFile, expectedName: string) {
  for (let parent = node.parent; parent; parent = parent.parent) {
    if ((ts.isJsxElement(parent) || ts.isJsxSelfClosingElement(parent))
      && jsxElementName(parent, sourceFile) === expectedName) return true;
  }
  return false;
}

function jsxElementName(node: ts.JsxElement | ts.JsxSelfClosingElement, sourceFile: ts.SourceFile) {
  return (ts.isJsxElement(node) ? node.openingElement.tagName : node.tagName).getText(sourceFile);
}

function imports(sourceFile: ts.SourceFile) {
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .map((statement) => (statement.moduleSpecifier as ts.StringLiteral).text);
}

function endpointLiterals(sourceFile: ts.SourceFile) {
  const values: string[] = [];
  const visit = (node: ts.Node) => {
    if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))
      && (node.text.startsWith("/") || /^(GET|POST|PUT|PATCH|DELETE)$/.test(node.text))) values.push(node.text);
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return values;
}

function moduleMutableDeclarations(sourceFile: ts.SourceFile) {
  return sourceFile.statements
    .filter(ts.isVariableStatement)
    .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0)
    .map((statement) => statement.getText());
}

function stringArrayLiterals(sourceFile: ts.SourceFile) {
  const arrays: string[][] = [];
  const visit = (node: ts.Node) => {
    if (ts.isArrayLiteralExpression(node)
      && node.elements.every((element) => ts.isStringLiteral(element))) {
      arrays.push(node.elements.map((element) => (element as ts.StringLiteral).text));
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return arrays;
}

function workerPilotFixtureProperties(sourceFile: ts.SourceFile) {
  for (const statement of sourceFile.statements) {
    if (!ts.isVariableStatement(statement)) continue;
    for (const declaration of statement.declarationList.declarations) {
      if (!ts.isIdentifier(declaration.name)
        || declaration.name.text !== "workerPilotRows"
        || !declaration.initializer
        || !ts.isArrayLiteralExpression(declaration.initializer)) continue;
      return declaration.initializer.elements
        .filter(ts.isObjectLiteralExpression)
        .map(objectPropertyNames);
    }
  }
  assert.fail("workerPilotRows browser fixture is missing");
}

function workerConfigurationNodeProperties(sourceFile: ts.SourceFile) {
  for (const statement of sourceFile.statements) {
    if (!ts.isFunctionDeclaration(statement)
      || statement.name?.text !== "workerConfiguration"
      || !statement.body) continue;
    for (const bodyStatement of statement.body.statements) {
      if (!ts.isReturnStatement(bodyStatement)
        || !bodyStatement.expression
        || !ts.isObjectLiteralExpression(bodyStatement.expression)) continue;
      const node = bodyStatement.expression.properties.find((property) =>
        ts.isPropertyAssignment(property)
          && property.name.getText() === "node");
      if (node && ts.isPropertyAssignment(node) && ts.isObjectLiteralExpression(node.initializer)) {
        return objectPropertyNames(node.initializer);
      }
    }
  }
  assert.fail("workerConfiguration browser fixture is missing");
}

function objectPropertyNames(object: ts.ObjectLiteralExpression) {
  return object.properties
    .filter((property) => ts.isPropertyAssignment(property) || ts.isShorthandPropertyAssignment(property))
    .map((property) => property.name.getText().replace(/^['"]|['"]$/g, ""));
}
