import assert from "node:assert/strict";
import test from "node:test";
import { createHarnessFixture } from "../helpers/browser-cdp-socket-fixture.mts";
import { nativeKeyTypeDiagnostics, formatDiagnostic } from "../helpers/browser-native-input-oracle.mts";

for (const direction of ["forward", "backward"] as const) {
  test(`UI-KEYBOARD-001-${direction}: real harness socket awaits ordered Tab down/up with exact modifiers`, async t => {
    const { harness, socket } = createHarnessFixture(); t.after(() => harness.close());
    socket.hold("Input.dispatchKeyEvent");
    const input = harness.pressTab(direction);
    const down = await socket.waitForCommand("Input.dispatchKeyEvent");
    assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, 1, "keyUp must wait for down acknowledgement");
    socket.respond(down, { result: {} }); await new Promise<void>(resolve => setImmediate(resolve));
    const commands = socket.commandsFor("Input.dispatchKeyEvent"); assert.equal(commands.length, 2);
    const base = { key: "Tab", code: "Tab", windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9, modifiers: direction === "backward" ? 8 : 0 };
    assert.deepEqual(commands.map(command => command.params), [{ ...base, type: "keyDown" }, { ...base, type: "keyUp" }]);
    assert.deepEqual(commands.map(command => command.sessionId), ["test-session", "test-session"]);
    socket.respond(commands[1], { result: {} }); await input;
    assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, 2);
  });
}
test("UI-KEYBOARD-002: unsupported direction is rejected at runtime and type boundary", async t => {
  const { harness, socket } = createHarnessFixture(); t.after(() => harness.close());
  for (const direction of ["Tab", "Shift+Tab", "", null, undefined, 8, {}]) await assert.rejects(Reflect.apply(harness.pressTab, harness, [direction]), /Unsupported Tab direction/);
  assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, 0);
  const diagnostics = nativeKeyTypeDiagnostics(`import { BrowserHarness } from "./browser-harness.mts"; declare const browser: BrowserHarness; browser.pressTab("forward"); browser.pressTab("backward"); browser.pressTab("Shift+Tab"); browser.pressNativeKey("Tab");`);
  assert.equal(diagnostics.length, 2, diagnostics.map(formatDiagnostic).join("\n")); assert.ok(diagnostics.every(diagnostic => diagnostic.code === 2345));
});
test("UI-KEYBOARD-003: closed and fatal harness reject without new sends", async () => {
  const closed = createHarnessFixture(); await closed.harness.close();
  for (const direction of ["forward", "backward"] as const) await assert.rejects(closed.harness.pressTab(direction), /Browser harness closed/);
  assert.equal(closed.socket.commandsFor("Input.dispatchKeyEvent").length, 0);
  const fatal = createHarnessFixture();
  try {
    fatal.socket.hold("Input.dispatchKeyEvent"); const input = fatal.harness.pressTab("backward");
    await fatal.socket.waitForCommand("Input.dispatchKeyEvent"); fatal.socket.close();
    await assert.rejects(input, /Browser CDP connection closed/);
    await assert.rejects(fatal.harness.pressTab("forward"), /Browser CDP connection closed/);
    assert.equal(fatal.socket.commandsFor("Input.dispatchKeyEvent").length, 1);
  } finally { await fatal.harness.close(); }
});
for (const failedType of ["keyDown", "keyUp"]) test(`UI-KEYBOARD-004-${failedType}: socket failure propagates without retry or extra events`, async t => {
  const { harness, socket } = createHarnessFixture(); t.after(() => harness.close()); socket.hold("Input.dispatchKeyEvent");
  const input = harness.pressTab("backward"), rejected = assert.rejects(input, /fixed Tab failure/);
  const down = await socket.waitForCommand("Input.dispatchKeyEvent");
  if (failedType === "keyDown") socket.respond(down, { error: { message: "fixed Tab failure" } });
  else { socket.respond(down, { result: {} }); await new Promise<void>(resolve => setImmediate(resolve)); socket.respond(socket.commandsFor("Input.dispatchKeyEvent")[1], { error: { message: "fixed Tab failure" } }); }
  await rejected; assert.equal(socket.commandsFor("Input.dispatchKeyEvent").length, failedType === "keyDown" ? 1 : 2);
});
