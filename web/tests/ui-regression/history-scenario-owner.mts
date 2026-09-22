import assert from "node:assert/strict";

// These are ownership boundaries, not new captures. Operations between the
// listed captures retain one document, fixture, controller and exact focus target.
const surfaces = ["streams", "resources", "application", "account", "archive", "monitoring", "incidents", "diagnostics", "remediation", "notifications"];
export const historicalScenarioPlan = Object.freeze([
  ...surfaces.flatMap(surface => [1440, 390].map(width => ({
    name: `${surface}-${width}`, captures: [`${surface}-${width}`], width, height: width === 1440 ? 900 : 844,
  }))),
  ...[
    ["streams-interactions", "streams-confirmation", "streams-cancel-focus", "streams-one-mutation"],
    ["streams-error", "streams-error"],
    ["streams-permission-denied", "streams-permission-denied"],
    ["streams-detail-preview", "streams-detail-preview", "streams-detail-closed"],
    ["resources-editor", "resources-editor", "resources-cancel-focus"],
    ["resources-permission-denied", "resources-permission-denied"],
    ["updater-settings", "updater-settings", "updater-cancel-focus"],
    ["account-secret-owner", "account-security", "account-secret-concealed", "account-secret-disposed"],
    ["archive-local", "archive-local"],
    ["archive-permission-denied", "archive-permission-denied"],
    ["monitoring-error-and-recovery", "monitoring-error", "monitoring-recovered"],
    ["mobile-navigation", "mobile-navigation", "mobile-navigation-closed"],
  ].map(([name, ...captures]) => ({ name: name!, captures, width: name === "mobile-navigation" ? 390 : 1440, height: name === "mobile-navigation" ? 844 : 900 })),
].map(row => Object.freeze({ ...row, captures: Object.freeze(row.captures) })));

export interface HistoricalBrowser {
  evaluate<T>(expression: string): Promise<T>;
  setViewport(width: number, height: number): Promise<void>;
  waitForRequestHandlersIdle(): Promise<void>;
  assertNoFatalError(): void;
  close(): Promise<void>;
}

const documents = new WeakMap<HistoricalBrowser, { claimed: boolean }>();

// Keep the fixed fixture's server state, but never share request evidence or a
// mutable state object with a browser that has completed its ownership lease.
export function nextHistoricalFixture<F extends { state: object }>(previous: F, create: () => F): F {
  const next = create();
  assert.notEqual(next, previous, "history fixture owner must be new");
  assert.notEqual(next.state, previous.state, "history fixture state must not alias the old owner");
  assert.deepEqual(Object.keys(next.state).sort(), Object.keys(previous.state).sort(), "fixed historical fixture state shape");
  Object.assign(next.state, structuredClone(previous.state));
  return next;
}

// Only a freshly launched, exclusively owned target may omit the old to-blank
// transition. This is never a classification or exception for a Fetch error.
export async function assertHistoricalFreshDocument(browser: HistoricalBrowser) {
  const grant = documents.get(browser);
  assert.ok(grant && !grant.claimed, "history document must be fresh and owned exactly once");
  grant.claimed = true;
  assert.equal(await browser.evaluate<boolean>("location.href === 'about:blank'"), true, "history target must start on its untouched blank document");
  assert.equal(documents.get(browser), grant, "history document ownership changed during observation");
  browser.assertNoFatalError();
}

export class HistoricalScenarioOwners<B extends HistoricalBrowser> {
  private initial: B | undefined;
  private active: B | undefined;
  private readonly seen = new WeakSet<B>();
  private readonly closed = new WeakMap<B, Promise<void>>();
  private running: Promise<void> | undefined;
  private shutdown: Promise<void> | undefined;
  private closing = false;
  private failure: { error: unknown } | undefined;
  private index = 0;
  private stopped = false;
  private readonly launch: () => Promise<B>;
  private readonly configure: (browser: B) => Promise<void>;
  private viewport = { width: 1440, height: 900 };
  private readonly captures: string[] = [];
  private readonly records: { scenario: string; captures: string[]; width: number; height: number; closed: boolean }[] = [];
  private current: (typeof this.records)[number] | undefined;

  constructor(first: B, launch: () => Promise<B>, configure: (browser: B) => Promise<void>) {
    this.initial = first;
    this.launch = launch;
    this.configure = configure;
  }

  async setViewport(width: number, height: number) {
    assert.ok((width === 1440 && height === 900) || (width === 390 && height === 844), "fixed historical viewport");
    this.viewport = { width, height };
    if (this.active) await this.active.setViewport(width, height);
  }

  async run(name: string, execute: (browser: B) => Promise<void>) {
    assert.equal(this.stopped, false, "history owner failure cannot be retried");
    assert.equal(this.closing, false, "history owner is closed");
    assert.equal(this.running, undefined, "history scenarios cannot overlap");
    const plan = historicalScenarioPlan[this.index];
    assert.equal(name, plan?.name, "history scenario order/identity");
    // Acquire before calling any asynchronous owner operation, including launch.
    // The barrier always resolves; run/close separately retain the same failure.
    let release!: () => void;
    this.running = new Promise<void>(done => { release = done; });
    try { await this.runOwned(name, execute); }
    catch (error) { this.stopped = true; this.failure = { error }; throw error; }
    finally { this.running = undefined; release(); }
  }

  private async runOwned(name: string, execute: (browser: B) => Promise<void>) {
    const plan = historicalScenarioPlan[this.index];
    const record = { scenario: name, captures: [] as string[], width: plan.width, height: plan.height, closed: false };
    let browser: B | undefined;
    let failed = false;
    let primary: unknown;
    try {
      const first = this.initial;
      this.initial = undefined;
      browser = first ?? await this.launch();
      assert.ok(!this.seen.has(browser), "history browser cannot be reused");
      this.seen.add(browser);
      assert.equal(this.closing, false, "history owner closed before execution");
      this.active = browser;
      documents.set(browser, { claimed: false });
      this.current = record;
      this.records.push(record);
      this.index += 1;
      if (!first) await this.configure(browser);
      assert.equal(this.closing, false, "history owner closed before execution");
      await browser.setViewport(this.viewport.width, this.viewport.height);
      assert.equal(this.closing, false, "history owner closed before execution");
      await execute(browser);
      await browser.waitForRequestHandlersIdle();
      browser.assertNoFatalError();
    } catch (error) { failed = true; primary = error; }
    // Closing happens after the original observations and handler settlement.
    // The run lease remains held until this one shared cleanup has completed.
    if (browser) documents.delete(browser);
    this.active = undefined;
    this.current = undefined;
    try { if (browser) { await this.closeOnce(browser); record.closed = true; } }
    catch (cleanup) {
      if (failed) throw new AggregateError([primary, cleanup], "history execution and owner cleanup both failed", { cause: primary });
      throw cleanup;
    }
    if (failed) throw primary;
  }

  recordCapture(name: string) {
    assert.ok(this.active && this.current, "capture requires an active history owner");
    const plan = historicalScenarioPlan[this.index - 1];
    assert.equal(name, plan.captures[this.current.captures.length], "capture belongs to this scenario in original order");
    assert.deepEqual(this.viewport, { width: plan.width, height: plan.height }, "capture viewport must retain the original scenario conditions");
    this.current.captures.push(name);
    this.captures.push(name);
  }

  finish() {
    assert.equal(this.running, undefined, "history owner completion pending");
    assert.equal(this.active, undefined);
    assert.equal(this.index, historicalScenarioPlan.length, "all historical owners must run");
    assert.deepEqual(this.captures, historicalScenarioPlan.flatMap(row => [...row.captures]), "all 41 captures in original order");
    assert.ok(this.records.every(record => record.closed), "all history cleanup must complete");
    return this.records.map(record => ({ ...record, captures: [...record.captures] }));
  }

  private closeOnce(browser: B) {
    const previous = this.closed.get(browser);
    if (previous) return previous;
    // Publish the completion before invoking close, including a synchronous throw.
    const completion = Promise.resolve().then(() => browser.close());
    this.closed.set(browser, completion);
    return completion;
  }

  close() {
    this.closing = true;
    this.shutdown ??= this.completeClose();
    return this.shutdown;
  }

  private async completeClose() {
    // Never make run wait for shutdown: it owns launch/body/settlement/cleanup,
    // while shutdown waits for that complete lease, including late launch results.
    if (this.running) await this.running;
    if (this.failure) throw this.failure.error;
    const browser = this.initial;
    this.initial = undefined;
    if (browser) {
      documents.delete(browser);
      try { await this.closeOnce(browser); }
      catch (error) { this.stopped = true; this.failure = { error }; throw error; }
    }
  }
}

export async function closeHistoricalOwner<B extends HistoricalBrowser>(owner: HistoricalScenarioOwners<B> | undefined, browser: B, primary?: { error: unknown }) {
  try { if (owner) await owner.close(); else await browser.close(); }
  catch (cleanup) {
    if (primary && primary.error === cleanup) throw cleanup;
    if (primary) throw new AggregateError([primary.error, cleanup], "history execution and owner cleanup both failed", { cause: primary.error });
    throw cleanup;
  }
}
