import { readBrowserSuiteSource } from "./read-browser-suite-source.mts";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import test from "node:test";
import { type BrowserNativeKey } from "./browser-harness.mts";
import { createHarnessFixture } from "./browser-cdp-socket-fixture.mts";
import { nativeKeyTypeDiagnostics, formatDiagnostic, browserNativeInputContractIssues, replaceExactlyOnce } from "./browser-native-input-oracle.mts";
import { browserHarnessPath, uiBrowserTestPath } from "./browser-lifecycle-source-paths.mts";


export function registerNativeInputCases() {


for (const expected of [
  {
    name: "Enter",
    key: "Enter",
    keyValue: "Enter",
    code: "Enter",
    virtualKeyCode: 13,
    text: "\r",
  },
  {
    name: "Space",
    key: "Space",
    keyValue: " ",
    code: "Space",
    virtualKeyCode: 32,
    text: " ",
  },
  {
    name: "Escape",
    key: "Escape",
    keyValue: "Escape",
    code: "Escape",
    virtualKeyCode: 27,
  },
] as const satisfies ReadonlyArray<{
  name: string;
  key: BrowserNativeKey;
  keyValue: string;
  code: string;
  virtualKeyCode: number;
  text?: string;
}>) {
  test(`BrowserHarness pressNativeKey dispatches ordered ${expected.name} key-down and key-up`, async (t) => {
    const { harness, socket } = createHarnessFixture();
    t.after(() => harness.close());

    await harness.pressNativeKey(expected.key);

    const commands = socket.commandsFor("Input.dispatchKeyEvent");
    const base = {
      key: expected.keyValue,
      code: expected.code,
      windowsVirtualKeyCode: expected.virtualKeyCode,
      nativeVirtualKeyCode: expected.virtualKeyCode,
    };
    assert.deepEqual(commands.map((command) => command.params), [
      {
        ...base,
        ...(expected.text === undefined ? {} : { text: expected.text, unmodifiedText: expected.text }),
        type: "keyDown",
      },
      { ...base, type: "keyUp" },
    ]);
    assert.deepEqual(commands.map((command) => command.sessionId), ["test-session", "test-session"]);
  });
}

test("BrowserHarness pressNativeKey propagates socket fatal errors without swallowing them", async (t) => {
  const { harness, socket } = createHarnessFixture();
  t.after(() => harness.close());
  socket.hold("Input.dispatchKeyEvent");

  const input = harness.pressNativeKey("Enter");
  await socket.waitForCommand("Input.dispatchKeyEvent");
  socket.close();

  await assert.rejects(input, /Browser CDP connection closed/);
  await assert.rejects(harness.pressNativeKey("Space"), /Browser CDP connection closed/);
});

test("BrowserHarness pressNativeKey rejects after close", async () => {
  const { harness, profile } = createHarnessFixture();
  await harness.close();
  assert.equal(existsSync(profile), false, "closed native-key fixture left its owned profile behind");
  await assert.rejects(harness.pressNativeKey("Escape"), /Browser harness closed/);
});

test("BrowserHarness native-key API is closed and rejects unsupported keys through TypeScript", () => {
  const diagnostics = nativeKeyTypeDiagnostics(`
    import { BrowserHarness } from "./browser-harness.mts";
    declare const browser: BrowserHarness;
    browser.pressNativeKey("Enter");
    browser.pressNativeKey("Space");
    browser.pressNativeKey("Escape");
    browser.pressNativeKey("Tab");
  `);
  assert.equal(diagnostics.length, 1, diagnostics.map(formatDiagnostic).join("\n"));
  assert.equal(diagnostics[0].code, 2345);
  assert.match(formatDiagnostic(diagnostics[0]), /"Tab"/);
});

test("BrowserHarness native-key source oracle rejects public/raw, widened, incomplete, swallowed and cast mutants", () => {
  const harnessSource = readFileSync(browserHarnessPath, "utf8");
  const uiBrowserSource = readBrowserSuiteSource(uiBrowserTestPath);
  assert.deepEqual(browserNativeInputContractIssues(harnessSource, uiBrowserSource), []);

  const mutants = [
    {
      name: "public generic send",
      harnessSource: replaceExactlyOnce(harnessSource, "  private async send(", "  async send("),
      uiBrowserSource,
      expectedIssue: "generic-public-cdp",
    },
    {
      name: "key union widened to string",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        'export type BrowserNativeKey = "Enter" | "Space" | "Escape";',
        "export type BrowserNativeKey = string;",
      ),
      uiBrowserSource,
      expectedIssue: "native-key-union",
    },
    {
      name: "Space omitted",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        'export type BrowserNativeKey = "Enter" | "Space" | "Escape";',
        'export type BrowserNativeKey = "Enter" | "Escape";',
      ),
      uiBrowserSource,
      expectedIssue: "native-key-union",
    },
    {
      name: "key-up omitted",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        '    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" });',
        "",
      ),
      uiBrowserSource,
      expectedIssue: "native-key-order",
    },
    {
      name: "fatal error swallowed",
      harnessSource: replaceExactlyOnce(
        harnessSource,
        '    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" });',
        '    await this.send("Input.dispatchKeyEvent", { ...base, type: "keyUp" }).catch(() => {});',
      ),
      uiBrowserSource,
      expectedIssue: "native-key-swallow",
    },
    {
      name: "UI browser unknown double cast",
      harnessSource,
      uiBrowserSource: `${uiBrowserSource}\nconst rawInput = browser as unknown as { send(method: string): Promise<void> };\n`,
      expectedIssue: "ui-private-input-bypass",
    },
  ];

  for (const mutant of mutants) {
    const issues = browserNativeInputContractIssues(mutant.harnessSource, mutant.uiBrowserSource);
    assert.equal(
      issues.includes(mutant.expectedIssue),
      true,
      `${mutant.name} was accepted by the native-key source oracle: ${JSON.stringify(issues)}`,
    );
  }
});
}
