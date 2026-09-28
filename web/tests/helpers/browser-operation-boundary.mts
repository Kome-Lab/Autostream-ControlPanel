/** Reference: G3-only operation deadline and narrowly owned dialog handling.
 * Not a Browser/Windows implementation or authority to run OS operations.
 * Integrate into the existing transport; never open a second CDP socket.
 */
export type BrowserOperationPhase = 'create-cancel' | 'edit-cancel' | 'worker-cancel' | 'read-state';
export type BrowserOperationOptions = Readonly<{
  phase: BrowserOperationPhase;
  mode: 'precheck' | 'observe-user';
  timeoutMs: number;
  url: string;
  discardLocale?: 'ja' | 'en';
}>;
export type BrowserOperationRecord = Readonly<Record<string, string | number | boolean | null>>;
export type BrowserOperationPort = Readonly<{
  write: (record: BrowserOperationRecord) => void;
  abort: (error: Error) => void;
  acceptDiscard: () => Promise<unknown>;
}>;
const discardCopy = Object.freeze({
  ja: '保存していない変更があります。破棄して移動しますか？「キャンセル」で入力を保持します。',
  en: 'You have unsaved changes. Discard them and leave? Cancel keeps your input.',
});
const phases = new Set<BrowserOperationPhase>(['create-cancel', 'edit-cancel', 'worker-cancel', 'read-state']);
const loggedMethods = new Set(['Runtime.evaluate', 'Input.dispatchMouseEvent', 'Input.dispatchKeyEvent',
  'Page.handleJavaScriptDialog', 'Page.captureScreenshot', 'Page.getLayoutMetrics']);

export class BrowserOperationBoundary {
  private readonly started = performance.now();
  private readonly options: BrowserOperationOptions;
  private readonly interrupted: Promise<never>;
  private rejectInterrupted!: (error: Error) => void;
  private timer: ReturnType<typeof setTimeout>;
  private active = true;
  private failure: Error | undefined;
  private ordinal = 0;
  private commands = 0;
  private dialogs = 0;
  private dialogOpen = false;
  private dialogCommand: Promise<void> | undefined;
  private readonly port: BrowserOperationPort;

  constructor(options: BrowserOperationOptions, port: BrowserOperationPort) {
    this.port = port;
    const url = new URL(options.url);
    if (!phases.has(options.phase) || !['precheck', 'observe-user'].includes(options.mode)
      || !Number.isFinite(options.timeoutMs) || options.timeoutMs <= 0
      || options.timeoutMs > (options.mode === 'observe-user' ? 4_200_000 : 10_000)
      || url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port
      || url.username || url.password || url.search
      || !['/admin/streams/', '/admin/workers/'].includes(url.pathname)) {
      throw new Error('Invalid G3 operation contract');
    }
    if (options.discardLocale !== undefined && (!['ja', 'en'].includes(options.discardLocale)
      || !['create-cancel', 'edit-cancel'].includes(options.phase) || url.pathname !== '/admin/streams/')) {
      throw new Error('Discard decision is only permitted for the current Streams cancel');
    }
    this.options = Object.freeze({ ...options });
    this.interrupted = new Promise<never>((_resolve, reject) => { this.rejectInterrupted = reject; });
    void this.interrupted.catch(() => {});
    this.timer = setTimeout(() => this.fail(new Error('G3_OPERATION_DEADLINE')), options.timeoutMs);
    this.emit('BEGIN');
  }

  private emit(event: string, fields: BrowserOperationRecord = {}, primary?: Error) {
    if (++this.ordinal > 512) return this.fail(new Error('G3_OPERATION_JOURNAL_BOUND'));
    try {
      this.port.write(Object.freeze({ sequence: this.ordinal, event, phase: this.options.phase,
        mode: this.options.mode, elapsedMs: Math.floor(performance.now() - this.started), ...fields }));
    } catch (error) {
      const writer = error instanceof Error ? error : new Error('G3 journal failure');
      this.fail(primary ? new AggregateError([primary, writer], 'G3 command and journal failure', { cause: primary }) : writer);
    }
  }

  fail(error: Error): Error {
    if (this.failure) return this.failure;
    this.failure = error;
    clearTimeout(this.timer);
    // Must reject real transport pending commands and block subsequent target sends.
    // This is not just Promise.race: port.abort is the existing fail-stop boundary.
    try { this.port.abort(error); }
    catch (abortError) { this.failure = new AggregateError([error, abortError], 'G3 failure and abort failure', { cause: error }); }
    this.rejectInterrupted(this.failure);
    return this.failure;
  }

  private assertActive() {
    if (this.failure) throw this.failure;
    if (!this.active) throw new Error('Closed G3 operation');
    if (performance.now() - this.started >= this.options.timeoutMs) throw this.fail(new Error('G3_OPERATION_DEADLINE'));
  }

  beforeCommand(method: string, params: Readonly<Record<string, unknown>>): number {
    this.assertActive();
    const command = ++this.commands;
    const type = params.type;
    this.emit('COMMAND_BEGIN', { command, method: loggedMethods.has(method) ? method : 'OTHER_CDP',
      inputType: typeof type === 'string' && ['mouseMoved','mousePressed','mouseReleased','keyDown','keyUp'].includes(type) ? type : null });
    this.assertActive();
    return command;
  }

  afterCommand(command: number, success: boolean, commandError?: Error) {
    if (!this.active || this.failure) return;
    this.emit('COMMAND_END', { command, success }, commandError);
    this.assertActive();
  }

  dialogOpening(params: Readonly<Record<string, unknown>>, exactSession: boolean) {
    if (!this.active || this.failure) return;
    const knownCopy = this.options.discardLocale !== undefined && params.message === discardCopy[this.options.discardLocale];
    const valid = exactSession && params.type === 'confirm' && params.url === this.options.url
      && knownCopy && this.dialogs === 0 && (params.defaultPrompt === undefined || params.defaultPrompt === '');
    this.emit('DIALOG_OPEN', { exactSession, expectedURL: params.url === this.options.url,
      confirmType: params.type === 'confirm', knownDiscardCopy: knownCopy, occurrence: this.dialogs + 1,
      hasBrowserHandler: typeof params.hasBrowserHandler === 'boolean' ? params.hasBrowserHandler : null });
    if (!valid) { this.fail(new Error('G3_UNEXPECTED_JAVASCRIPT_DIALOG')); return; }
    this.dialogs++;
    this.dialogOpen = true;
    if (this.options.mode === 'observe-user') return; // User chooses; no synthetic receipt.
    try {
      this.assertActive();
      this.dialogCommand = this.port.acceptDiscard().then(() => {
        if (!this.failure && this.active) this.emit('DISCARD_COMMAND_ACK');
      }, error => { throw this.fail(error instanceof Error ? error : new Error('G3 dialog command failure')); });
      void this.dialogCommand.catch(() => {});
    } catch (error) { this.fail(error instanceof Error ? error : new Error('G3 dialog command failure')); }
  }

  dialogClosed(params: Readonly<Record<string, unknown>>, exactSession: boolean) {
    if (!this.active || this.failure) return;
    if (!exactSession || !this.dialogOpen || typeof params.result !== 'boolean') {
      this.fail(new Error('G3_UNEXPECTED_DIALOG_CLOSE')); return;
    }
    this.emit('DIALOG_CLOSED', { accepted: params.result, actor: this.options.mode === 'precheck' ? 'g3-precheck' : 'user' });
    this.dialogOpen = false;
    if (this.options.mode === 'precheck' && params.result !== true) this.fail(new Error('G3_DISCARD_NOT_CONFIRMED'));
  }

  async run<T>(action: () => Promise<T>): Promise<T> {
    try {
      this.assertActive();
      // Start only after the scope is installed by the host, before first input.
      const value = await Promise.race([Promise.resolve().then(() => { this.assertActive(); return action(); }), this.interrupted]);
      if (this.dialogCommand) await Promise.race([this.dialogCommand, this.interrupted]);
      this.assertActive();
      if (this.dialogOpen) throw this.fail(new Error('G3_DIALOG_NOT_CLOSED'));
      this.emit('END', { dialogs: this.dialogs });
      this.assertActive();
      return value;
    } catch (error) {
      const primary = error instanceof Error ? error : new Error('G3 operation failed');
      throw this.failure ?? this.fail(primary);
    } finally {
      this.active = false;
      clearTimeout(this.timer);
    }
  }
}
