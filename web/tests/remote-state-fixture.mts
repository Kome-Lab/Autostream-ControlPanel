import assert from "node:assert/strict";
import { Children, createElement, isValidElement, type ReactElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { register } from "node:module";
import { fileURLToPath } from "node:url";
import type { AdaptedAPIError } from "../src/lib/foundation/api-errors/contracts.ts";
import type { Freshness, RemoteState } from "../src/lib/foundation/remote-state/contracts.ts";
import type { RemoteSectionState } from "../src/lib/foundation/remote-state/aggregate.ts";
import type { RemoteStateBoundaryProps } from "../src/components/foundation/remote-state/remote-state-boundary.ts";
import type { TranslationKey, TranslationValues } from "../src/lib/i18n.ts";


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
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

type ProjectorModule = typeof import("../src/lib/foundation/remote-state/projector.ts");
type AggregateModule = typeof import("../src/lib/foundation/remote-state/aggregate.ts");
type BoundaryModule = typeof import("../src/components/foundation/remote-state/remote-state-boundary.ts");
type NoticeModule = typeof import("../src/components/foundation/remote-state/remote-state-notice.ts");
type I18nModule = typeof import("../src/lib/i18n.ts");

export const webRoot = fileURLToPath(new URL("..", import.meta.url));
const protocolError = Object.freeze({ kind: "protocol", messageKey: "apiErrorProtocol" } as const satisfies AdaptedAPIError);
export const networkError = Object.freeze({ kind: "network", messageKey: "apiErrorNetwork" } as const satisfies AdaptedAPIError);
export const unavailableError = Object.freeze({ kind: "unavailable", messageKey: "apiErrorUnavailable" } as const satisfies AdaptedAPIError);

let projectorPromise: Promise<ProjectorModule> | undefined;
let aggregatePromise: Promise<AggregateModule> | undefined;
let boundaryPromise: Promise<BoundaryModule> | undefined;
let noticePromise: Promise<NoticeModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

export function loadProjector() {
  projectorPromise ??= import("../src/lib/foundation/remote-state/projector.ts");
  return projectorPromise;
}

export function loadAggregate() {
  aggregatePromise ??= import("../src/lib/foundation/remote-state/aggregate.ts");
  return aggregatePromise;
}

export function loadBoundary() {
  boundaryPromise ??= import("../src/components/foundation/remote-state/remote-state-boundary.ts");
  return boundaryPromise;
}

export function loadNotice() {
  noticePromise ??= import("../src/components/foundation/remote-state/remote-state-notice.ts");
  return noticePromise;
}

export function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

export function fresh(lastSuccessAt: number): Freshness {
  return { kind: "fresh", lastSuccessAt };
}

export function refreshing(lastSuccessAt: number): Freshness {
  return { kind: "refreshing", lastSuccessAt };
}

export function stale(lastSuccessAt: number, error: AdaptedAPIError): Freshness {
  return { kind: "stale", lastSuccessAt, error };
}

export function ready<T>(data: T, freshness: Freshness): RemoteState<T> {
  return { kind: "ready", data, freshness };
}

export function empty(freshness: Freshness): RemoteState<unknown> {
  return { kind: "empty", freshness };
}

export function section(id: string, state: RemoteState<unknown>): RemoteSectionState {
  return { id, state };
}

export function counts(totalCount: number, knownCount: number, positiveCount: number, negativeCount: number, unknownCount: number) {
  return { totalCount, knownCount, positiveCount, negativeCount, unknownCount };
}

export function projectRuntime(module: ProjectorModule, snapshot: unknown, options: unknown): unknown {
  return Reflect.apply(module.projectRemoteState, undefined, [snapshot, options]);
}

export function aggregateRuntime(module: AggregateModule, input: unknown): unknown {
  return Reflect.apply(module.aggregateRemoteState, undefined, [input]);
}

export function coverageRuntime(module: AggregateModule, contributions: readonly unknown[]): unknown {
  return Reflect.apply(module.summarizeRemoteCoverage, undefined, [contributions]);
}

export function assertProtocolBlocking(value: unknown, label: string) {
  assert.deepEqual(value, { kind: "blocking-error", error: protocolError }, label);
  assert.equal(Object.isFrozen(value), true, `${label} wrapper`);
}

export function boundaryCallbacks<T>(i18n: I18nModule, renderData: RemoteStateBoundaryProps<T>["renderData"]): Omit<RemoteStateBoundaryProps<T>, "state"> {
  return {
    noticeId: "remote-state-notice",
    translate: english(i18n),
    formatTimestamp: String,
    renderData,
  };
}

export function english(i18n: I18nModule) {
  return (key: TranslationKey, values?: TranslationValues) => i18n.translate("en", key, values);
}

export function renderBoundary<T>(
  Boundary: BoundaryModule["RemoteStateBoundary"],
  props: RemoteStateBoundaryProps<T>,
) {
  return renderToStaticMarkup(createElement(Boundary, props));
}

type ButtonProps = Readonly<{
  children?: ReactNode;
  disabled?: boolean;
  onClick?: () => void;
  "aria-busy"?: boolean;
  "aria-describedby"?: string;
  "aria-disabled"?: boolean;
}>;

export function requireButton(node: ReactNode): ReactElement<ButtonProps> {
  const button = findButton(node);
  assert.ok(button, "retry button is missing");
  return button;
}

function findButton(node: ReactNode): ReactElement<ButtonProps> | undefined {
  for (const child of Children.toArray(node)) {
    if (!isValidElement<ButtonProps>(child)) continue;
    if (child.type === "button") return child;
    const nested = findButton(child.props.children);
    if (nested) return nested;
  }
  return undefined;
}

export function assertNoMarkers(markup: string, markers: readonly string[]) {
  for (const marker of markers) assert.equal(markup.includes(marker), false, marker);
}

export function countOccurrences(value: string, needle: string) {
  return value.split(needle).length - 1;
}

export function assertOracleRejects(
  label: string,
  oracle: (value: unknown) => void,
  real: unknown,
  mutated: unknown,
) {
  assert.doesNotThrow(() => oracle(real), `${label} real implementation`);
  assert.throws(() => oracle(mutated), undefined, `${label} mutation must be detected`);
}

export function assertInitial(value: unknown) {
  assert.deepEqual(value, { kind: "initial-loading" });
}

export function assertCachedVisible(value: unknown, expectedData: unknown) {
  assert.equal(isObjectLike(value) && Reflect.get(value, "kind"), "ready");
  assert.equal(isObjectLike(value) && Reflect.get(value, "data"), expectedData);
}

export function assertPartial(value: unknown) {
  assert.equal(isObjectLike(value) && Reflect.get(value, "kind"), "partial");
}

export function assertStale(value: unknown) {
  assert.equal(isObjectLike(value) && isObjectLike(Reflect.get(value, "freshness")) && Reflect.get(Reflect.get(value, "freshness"), "kind"), "stale");
}

export function assertMissing(value: unknown, expected: string) {
  assert.equal(isObjectLike(value), true);
  if (!isObjectLike(value)) return;
  const missing = Reflect.get(value, "missingSections");
  assert.equal(Array.isArray(missing) && missing.includes(expected), true);
}

export function assertUnknownExcluded(value: unknown) {
  assert.equal(isObjectLike(value), true);
  if (!isObjectLike(value)) return;
  assert.equal(Reflect.get(value, "knownCount"), 0);
  assert.equal(Reflect.get(value, "positiveCount"), 0);
  assert.equal(Reflect.get(value, "unknownCount"), 1);
}

export function assertMarkupContains(value: unknown, expected: string) {
  assert.equal(typeof value === "string" && value.includes(expected), true);
}

export function assertMarkupOmits(value: unknown, marker: string) {
  assert.equal(typeof value === "string" && value.includes(marker), false);
}

export function assertRetryNotInvokedDuringRender(render: (onRetry: () => void) => void) {
  let calls = 0;
  render(() => {
    calls += 1;
  });
  assert.equal(calls, 0);
}

export function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}
