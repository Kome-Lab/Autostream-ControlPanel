import ts from "typescript";


export const workerRestartFocusOutcomes = ["403", "409", "outcome_unknown"] as const;
export type WorkerRestartFocusOutcome = typeof workerRestartFocusOutcomes[number];
type WorkerRestartFocusStage = "real-focus" | "containment" | "escape" | "focus-return";
export type OutcomeTruth = true | false | "unknown";
export type FocusValue =
  | { kind: "real"; origin?: ts.Symbol }
  | { kind: "synthetic" }
  | { kind: "unknown" };

export type WorkerRestartFocusIssue = {
  outcome: WorkerRestartFocusOutcome;
  stage: WorkerRestartFocusStage;
  reason: string;
  position: number;
  line: number;
  column: number;
};

export type WorkerRestartFocusState = {
  values: Map<ts.Symbol, FocusValue>;
  containment: Map<ts.Symbol, Set<string>>;
  dialogReady: boolean;
  renderedWait: boolean;
  renderedOutcome: boolean;
  realCallSeen: boolean;
  escaped: boolean;
  focusReturned: boolean;
};

export type WorkerRestartFocusContext = {
  sourceFile: ts.SourceFile;
  checker: ts.TypeChecker;
  outcome: WorkerRestartFocusOutcome;
  outcomeVariable: string;
  issues: WorkerRestartFocusIssue[];
};

export const workerRestartFocusExpectations = new Map<string, boolean | number>([
  ["dialogCount", 1],
  ["activeExists", true],
  ["activeInside", true],
  ["activeIsBody", false],
  ["activeIsTrigger", false],
  ["activeHiddenOrInert", false],
  ["activeVisible", true],
  ["safeOutcomeTextVisible", true],
]);

export function newWorkerRestartFocusState(): WorkerRestartFocusState {
  return {
    values: new Map(),
    containment: new Map(),
    dialogReady: false,
    renderedWait: false,
    renderedOutcome: false,
    realCallSeen: false,
    escaped: false,
    focusReturned: false,
  };
}

export function cloneWorkerRestartFocusState(state: WorkerRestartFocusState): WorkerRestartFocusState {
  return {
    ...state,
    values: new Map(state.values),
    containment: new Map([...state.containment].map(([symbol, fields]) => [symbol, new Set(fields)])),
  };
}

export function retainWorkerRestartFocusValue(state: WorkerRestartFocusState, symbol: ts.Symbol, value: FocusValue) {
  state.values.set(symbol, value.kind === "real" && !value.origin ? { ...value, origin: symbol } : value);
}

export function workerRestartFocusEarlyExit(
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
  node: ts.Node,
  reason: string,
) {
  for (const state of states) {
    const stage = firstMissingWorkerRestartFocusStage(state);
    if (stage) addWorkerRestartFocusIssue(context, stage, reason, node);
  }
  return [];
}

export function workerRestartFocusUnsupportedControl(
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
  node: ts.Node,
  reason: string,
) {
  const remaining: WorkerRestartFocusState[] = [];
  for (const state of states) {
    const stage = firstMissingWorkerRestartFocusStage(state);
    if (stage) addWorkerRestartFocusIssue(context, stage, reason, node);
    else remaining.push(state);
  }
  return remaining;
}

export function validateWorkerRestartFocusCompletion(
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
  node: ts.Node,
) {
  const stage = firstMissingWorkerRestartFocusStage(state);
  if (!stage) return;
  let reason = "missing-focus-return";
  if (stage === "real-focus") reason = state.realCallSeen ? "result-not-retained" : "missing-real-focus";
  if (stage === "containment") {
    reason = `missing-containment:${missingWorkerRestartContainmentFields(state).join(",")}`;
  }
  if (stage === "escape") reason = "missing-escape";
  addWorkerRestartFocusIssue(context, stage, reason, node);
}

function firstMissingWorkerRestartFocusStage(state: WorkerRestartFocusState): WorkerRestartFocusStage | undefined {
  if (!hasWorkerRestartRealFocus(state)) return "real-focus";
  if (!hasWorkerRestartCompleteContainment(state)) return "containment";
  if (!state.escaped) return "escape";
  if (!state.focusReturned) return "focus-return";
  return undefined;
}

export function hasWorkerRestartRealFocus(state: WorkerRestartFocusState) {
  return [...state.values.values()].some((value) => value.kind === "real" && value.origin);
}

export function hasWorkerRestartCompleteContainment(state: WorkerRestartFocusState) {
  return [...state.values.entries()].some(([symbol, value]) =>
    value.kind === "real"
      && value.origin === symbol
      && workerRestartFocusExpectations.size === (state.containment.get(symbol)?.size ?? 0));
}

function missingWorkerRestartContainmentFields(state: WorkerRestartFocusState) {
  const best = [...state.values.entries()]
    .filter(([symbol, value]) => value.kind === "real" && value.origin === symbol)
    .map(([symbol]) => state.containment.get(symbol) ?? new Set<string>())
    .sort((left, right) => right.size - left.size)[0] ?? new Set<string>();
  return [...workerRestartFocusExpectations.keys()].filter((field) => !best.has(field));
}

export function addWorkerRestartFocusIssue(
  context: WorkerRestartFocusContext,
  stage: WorkerRestartFocusStage,
  reason: string,
  node: ts.Node,
) {
  const position = node.getStart(context.sourceFile);
  const location = context.sourceFile.getLineAndCharacterOfPosition(position);
  context.issues.push({
    outcome: context.outcome,
    stage,
    reason,
    position,
    line: location.line + 1,
    column: location.character + 1,
  });
}

export function formatWorkerRestartFocusIssues(issues: WorkerRestartFocusIssue[]) {
  const outcomeOrder = new Map(workerRestartFocusOutcomes.map((outcome, index) => [outcome, index]));
  const stageOrder = new Map<WorkerRestartFocusStage, number>([
    ["real-focus", 0],
    ["containment", 1],
    ["escape", 2],
    ["focus-return", 3],
  ]);
  const unique = new Map<string, WorkerRestartFocusIssue>();
  for (const issue of issues) {
    const key = `${issue.outcome}\0${issue.stage}\0${issue.reason}\0${issue.position}`;
    if (!unique.has(key)) unique.set(key, issue);
  }
  return [...unique.values()]
    .sort((left, right) =>
      (outcomeOrder.get(left.outcome) ?? 99) - (outcomeOrder.get(right.outcome) ?? 99)
        || left.position - right.position
        || (stageOrder.get(left.stage) ?? 99) - (stageOrder.get(right.stage) ?? 99)
        || left.reason.localeCompare(right.reason))
    .map((issue) =>
      `outcome=${issue.outcome};stage=${issue.stage};reason=${issue.reason};line=${issue.line};column=${issue.column}`);
}
