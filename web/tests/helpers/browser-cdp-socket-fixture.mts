import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BrowserHarness } from "./browser-harness.mts";


export async function settlePromptly(promise: Promise<unknown>) {
  return Promise.race([
    promise.then(
      () => new Error("waiter unexpectedly resolved"),
      (error) => error,
    ),
    new Promise<"pending">((resolvePending) => setTimeout(() => resolvePending("pending"), 20)),
  ]);
}

type FakeCDPCommand = {
  id: number;
  method: string;
  params: Record<string, unknown>;
  sessionId?: string;
};

class FakeCDPSocket extends EventTarget {
  autoLoadEvent = true;
  private readonly commands: FakeCDPCommand[] = [];
  private readonly heldMethods = new Set<string>();
  private readonly commandWaiters = new Map<string, Array<(command: FakeCDPCommand) => void>>();

  hold(method: string) {
    this.heldMethods.add(method);
  }

  send(rawMessage: string) {
    const command = JSON.parse(rawMessage) as FakeCDPCommand;
    this.commands.push(command);
    const waiter = this.commandWaiters.get(command.method)?.shift();
    if (waiter) waiter(command);
    if (this.heldMethods.has(command.method)) return;
    queueMicrotask(() => {
      this.respond(command, { result: {} });
      if (command.method === "Page.navigate" && this.autoLoadEvent) {
        this.emitEvent("Page.loadEventFired", { timestamp: 1 });
      }
    });
  }

  close() {
    this.dispatchEvent(new Event("close"));
  }

  emitEvent(method: string, params: Record<string, unknown>) {
    this.emitMessage({ method, params, sessionId: "test-session" });
  }

  respond(command: FakeCDPCommand, response: { result?: Record<string, unknown>; error?: { message: string } }) {
    this.emitMessage({ id: command.id, sessionId: command.sessionId, ...response });
  }

  waitForCommand(method: string) {
    const existing = this.commands.find((command) => command.method === method);
    if (existing) return Promise.resolve(existing);
    return new Promise<FakeCDPCommand>((resolveCommand) => {
      const waiters = this.commandWaiters.get(method) || [];
      waiters.push(resolveCommand);
      this.commandWaiters.set(method, waiters);
    });
  }

  commandsFor(method: string) {
    return this.commands.filter((command) => command.method === method);
  }

  private emitMessage(message: Record<string, unknown>) {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(message) }));
  }
}

export function createHarnessFixture() {
  const socket = new FakeCDPSocket();
  const profile = mkdtempSync(join(tmpdir(), "autostream-ui-browser-"));
  const browserProcess = { exitCode: 0, signalCode: null };
  const harness = Reflect.construct(
    BrowserHarness,
    [browserProcess, profile, socket as unknown as WebSocket, "test-session"],
  ) as BrowserHarness;
  return { harness, profile, socket };
}
