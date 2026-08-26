import assert from "node:assert/strict";
import test from "node:test";

import {
  buildBrowserLaunchProfile,
  classifyDbusSessionBusAddress,
  classifyXdgRuntimeDirectory,
  collectBrowserLaunchFacts,
  formatBrowserLaunchFacts,
  isValidDbusEscapedValue,
  sanitizeBrowserDiagnosticText,
} from "./browser-launch-profile.mts";

const requiredLinuxFlags = [
  "--headless",
  "--password-store=basic",
  "--disable-dev-shm-usage",
  "--disable-hang-monitor",
  "--disable-popup-blocking",
  "--disable-prompt-on-repost",
  "--disable-sync",
  "--metrics-recording-only",
  "--mute-audio",
  "--window-size=1280,1024",
] as const;

const linuxOnlyFlags = requiredLinuxFlags.filter((flag) => flag !== "--headless");

test("canonical Linux profile is loopback-only, unique, and bounded by attempt", () => {
  const first = buildBrowserLaunchProfile({
    platform: "linux",
    attemptNumber: 1,
    userDataDirectory: "/owned/attempt-one/profile",
    xdgRuntimeDirectory: "/owned/attempt-one/xdg-runtime",
    parentEnvironment: {
      PATH: "/usr/bin",
      DBUS_SESSION_BUS_ADDRESS: "unix:path=/run/user/1000/bus",
    },
  });
  const second = buildBrowserLaunchProfile({
    platform: "linux",
    attemptNumber: 2,
    userDataDirectory: "/owned/attempt-two/profile",
    xdgRuntimeDirectory: "/owned/attempt-two/xdg-runtime",
    parentEnvironment: { PATH: "/usr/bin" },
  });

  for (const flag of requiredLinuxFlags) {
    assert.equal(first.args.filter((argument) => argument === flag).length, 1, `${flag} first attempt count`);
    assert.equal(second.args.filter((argument) => argument === flag).length, 1, `${flag} second attempt count`);
  }
  for (const profile of [first, second]) {
    assert.equal(profile.args.includes("--headless=new"), false);
    assert.equal(profile.args.includes("--no-sandbox"), false);
    assert.equal(profile.args.includes("--disable-setuid-sandbox"), false);
    assert.equal(profile.args.filter((argument) => argument === "--remote-debugging-address=127.0.0.1").length, 1);
    assert.equal(profile.args.filter((argument) => argument === "--remote-debugging-port=0").length, 1);
    assert.equal(new Set(profile.args).size, profile.args.length, "duplicate browser arguments");
    assert.equal(profile.args.at(-1), "about:blank");
  }
  assert.equal(first.args.includes("--enable-logging=stderr"), false);
  assert.equal(first.args.includes("--v=1"), false);
  assert.equal(second.args.filter((argument) => argument === "--enable-logging=stderr").length, 1);
  assert.equal(second.args.filter((argument) => argument === "--v=1").length, 1);
  assert.ok(first.args.includes("--user-data-dir=/owned/attempt-one/profile"));
  assert.ok(second.args.includes("--user-data-dir=/owned/attempt-two/profile"));
  assert.notEqual(first.environment.XDG_RUNTIME_DIR, second.environment.XDG_RUNTIME_DIR);
  assert.equal(first.environment.XDG_RUNTIME_DIR, "/owned/attempt-one/xdg-runtime");
  assert.equal(second.environment.XDG_RUNTIME_DIR, "/owned/attempt-two/xdg-runtime");
});

test("Windows profile keeps safe common launch flags without Linux-only flags", () => {
  const profile = buildBrowserLaunchProfile({
    platform: "win32",
    attemptNumber: 1,
    userDataDirectory: "C:\\owned\\attempt\\profile",
    parentEnvironment: { SystemRoot: "C:\\Windows", TEMP: "C:\\Temp" },
  });

  assert.ok(profile.args.includes("--headless"));
  assert.equal(profile.args.includes("--headless=new"), false);
  for (const flag of linuxOnlyFlags) assert.equal(profile.args.includes(flag), false, `${flag} must remain Linux-only`);
  assert.equal(profile.args.includes("--no-sandbox"), false);
  assert.equal(profile.args.includes("--disable-setuid-sandbox"), false);
  assert.ok(profile.args.includes("--remote-debugging-address=127.0.0.1"));
  assert.ok(profile.args.includes("--remote-debugging-port=0"));
  assert.equal(profile.environment.XDG_RUNTIME_DIR, undefined);
});

test("D-Bus raw reserved bytes are rejected and omitted from the child environment", async (t) => {
  const fixtures = [
    { name: "raw colon", value: "unix:path=/tmp/dbus:bad" },
    { name: "raw at sign", value: "unix:path=/tmp/dbus@bad" },
  ] as const;

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      const parentEnvironment = {
        PATH: "/usr/bin",
        AUTOSTREAM_UNRELATED: "preserved",
        DBUS_SESSION_BUS_ADDRESS: fixture.value,
      };
      const parentSnapshot = { ...parentEnvironment };

      const classification = classifyDbusSessionBusAddress(fixture.value);
      const profile = buildBrowserLaunchProfile({
        platform: "linux",
        attemptNumber: 1,
        userDataDirectory: "/owned/profile",
        xdgRuntimeDirectory: "/owned/xdg-runtime",
        parentEnvironment,
      });

      assert.equal(classification, "invalid");
      assert.equal(Object.hasOwn(profile.environment, "DBUS_SESSION_BUS_ADDRESS"), false);
      assert.equal(profile.environment.AUTOSTREAM_UNRELATED, "preserved");
      assert.deepEqual(parentEnvironment, parentSnapshot);
    });
  }
});

test("D-Bus encoded values accept the exact unescaped ASCII byte set", async (t) => {
  const fixtures = [
    { name: "combined categories", value: "-09AZaz_/.*" },
    { name: "hyphen", value: "-" },
    { name: "digit lower boundary", value: "0" },
    { name: "digit upper boundary", value: "9" },
    { name: "uppercase lower boundary", value: "A" },
    { name: "uppercase upper boundary", value: "Z" },
    { name: "lowercase lower boundary", value: "a" },
    { name: "lowercase upper boundary", value: "z" },
    { name: "underscore", value: "_" },
    { name: "slash", value: "/" },
    { name: "dot", value: "." },
    { name: "asterisk", value: "*" },
  ] as const;

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      assert.equal(isValidDbusEscapedValue(fixture.value), true);
    });
  }
});

test("D-Bus percent escapes remain encoded and byte-for-byte unchanged", async (t) => {
  const fixtures = [
    { name: "colon", value: "/tmp/dbus%3Abad" },
    { name: "at sign", value: "/tmp/dbus%40bad" },
    { name: "comma", value: "/tmp/a%2Cb" },
    { name: "semicolon", value: "/tmp/a%3Bb" },
    { name: "equals", value: "/tmp/a%3Db" },
    { name: "percent", value: "/tmp/a%25b" },
    { name: "space", value: "/tmp/a%20b" },
    { name: "UTF-8 bytes", value: "/tmp/%E3%81%82" },
    { name: "lowercase hex", value: "/tmp/a%2fb" },
    { name: "uppercase hex", value: "/tmp/a%2Fb" },
    { name: "combined reserved bytes", value: "/tmp/a%2Cb%3Bc%3Dd%25e" },
  ] as const;

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      const address = `unix:path=${fixture.value}`;
      assert.equal(isValidDbusEscapedValue(fixture.value), true);
      assert.equal(classifyDbusSessionBusAddress(address), "valid-unix");
      const profile = buildBrowserLaunchProfile({
        platform: "linux",
        attemptNumber: 1,
        userDataDirectory: "/owned/profile",
        xdgRuntimeDirectory: "/owned/xdg-runtime",
        parentEnvironment: { DBUS_SESSION_BUS_ADDRESS: address },
      });
      assert.equal(profile.environment.DBUS_SESSION_BUS_ADDRESS, address);
    });
  }
});

test("D-Bus encoded values reject every raw reserved byte and malformed percent escape", async (t) => {
  const fixtures = [
    { name: "empty", value: "" },
    { name: "raw colon", value: "/tmp/a:b" },
    { name: "raw at sign", value: "/tmp/a@b" },
    { name: "raw comma", value: "/tmp/a,b" },
    { name: "raw semicolon", value: "/tmp/a;b" },
    { name: "raw equals", value: "/tmp/a=b" },
    { name: "bare percent", value: "/tmp/a%" },
    { name: "raw space", value: "/tmp/a b" },
    { name: "raw tab", value: "/tmp/a\tb" },
    { name: "raw carriage return", value: "/tmp/a\rb" },
    { name: "raw line feed", value: "/tmp/a\nb" },
    { name: "raw NUL", value: "/tmp/a\u0000b" },
    { name: "raw non-ASCII", value: "/tmp/あ" },
    { name: "one hex digit", value: "/tmp/a%0" },
    { name: "non-hex then digit", value: "/tmp/a%G0" },
    { name: "digit then non-hex", value: "/tmp/a%0G" },
    { name: "two non-hex digits", value: "/tmp/a%GG" },
    { name: "trailing incomplete escape", value: "/tmp/a%4" },
  ] as const;

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      assert.equal(isValidDbusEscapedValue(fixture.value), false);
      assert.equal(classifyDbusSessionBusAddress(`unix:path=${fixture.value}`), "invalid");
    });
  }
});

test("D-Bus structure keeps delimiters structural and retains single-address policy", async (t) => {
  const fixtures = [
    { name: "transport colon and key equals", value: "unix:path=/tmp/dbus", classification: "valid-unix" },
    { name: "tcp field comma", value: "tcp:host=127.0.0.1,port=4713", classification: "valid-tcp" },
    { name: "raw colon in value", value: "unix:path=/tmp/a:b", classification: "invalid" },
    { name: "escaped colon in value", value: "unix:path=/tmp/a%3Ab", classification: "valid-unix" },
    { name: "raw equals in value", value: "unix:path=/tmp/a=b", classification: "invalid" },
    { name: "escaped equals in value", value: "unix:path=/tmp/a%3Db", classification: "valid-unix" },
    { name: "raw comma in value", value: "unix:path=/tmp/a,b", classification: "invalid" },
    { name: "escaped comma in value", value: "unix:path=/tmp/a%2Cb", classification: "valid-unix" },
    { name: "semicolon address list remains unsupported", value: "unix:path=/tmp/a;unix:path=/tmp/b", classification: "invalid" },
    { name: "escaped semicolon in value", value: "unix:path=/tmp/a%3Bb", classification: "valid-unix" },
  ] as const;

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      assert.equal(classifyDbusSessionBusAddress(fixture.value), fixture.classification);
    });
  }
});

test("D-Bus boundary fixtures detect permissive validator and child-environment mutants", async (t) => {
  const rejectedValues = [":", "@", "%", "あ"] as const;
  const assertRejected = (validator: (value: string) => boolean) => {
    for (const value of rejectedValues) assert.equal(validator(value), false);
  };
  assertRejected(isValidDbusEscapedValue);

  const validatorMutants = [
    { name: "raw colon allowed", accepted: ":" },
    { name: "raw at sign allowed", accepted: "@" },
    { name: "incomplete percent allowed", accepted: "%" },
    { name: "raw Unicode allowed", accepted: "あ" },
  ] as const;
  for (const mutant of validatorMutants) {
    await t.test(mutant.name, () => {
      const mutatedValidator = (value: string) => value === mutant.accepted || isValidDbusEscapedValue(value);
      assert.throws(() => assertRejected(mutatedValidator), { code: "ERR_ASSERTION" });
    });
  }

  await t.test("invalid address copied to child environment", () => {
    const invalidAddress = "unix:path=/tmp/dbus:bad";
    const profile = buildBrowserLaunchProfile({
      platform: "linux",
      attemptNumber: 1,
      userDataDirectory: "/owned/profile",
      xdgRuntimeDirectory: "/owned/xdg-runtime",
      parentEnvironment: { DBUS_SESSION_BUS_ADDRESS: invalidAddress },
    });
    const assertOmitted = (environment: NodeJS.ProcessEnv) => {
      assert.equal(Object.hasOwn(environment, "DBUS_SESSION_BUS_ADDRESS"), false);
    };
    assertOmitted(profile.environment);
    const copiedMutation = { ...profile.environment, DBUS_SESSION_BUS_ADDRESS: invalidAddress };
    assert.throws(() => assertOmitted(copiedMutation), { code: "ERR_ASSERTION" });
  });
});

test("D-Bus classification preserves only a valid unix or tcp address without diagnosing its value", async (t) => {
  const fixtures = [
    { name: "unix", value: "unix:path=/run/user/1000/bus", classification: "valid-unix", preserved: true },
    { name: "unix encoded colon", value: "unix:path=/tmp/dbus%3Abad", classification: "valid-unix", preserved: true },
    { name: "unix abstract", value: "unix:abstract=/tmp/dbus-AbCd123", classification: "valid-unix", preserved: true },
    { name: "tcp", value: "tcp:host=127.0.0.1,port=4713", classification: "valid-tcp", preserved: true },
    { name: "absent", value: undefined, classification: "absent", preserved: false },
    { name: "unsupported transport", value: "nonce-tcp:host=127.0.0.1,port=4713", classification: "invalid", preserved: false },
    { name: "missing unix value", value: "unix:path=", classification: "invalid", preserved: false },
    { name: "invalid tcp port", value: "tcp:host=127.0.0.1,port=70000", classification: "invalid", preserved: false },
    { name: "multiple addresses", value: "unix:path=/run/user/1000/bus;tcp:host=127.0.0.1,port=4713", classification: "invalid", preserved: false },
    { name: "raw colon", value: "unix:path=/tmp/dbus:bad", classification: "invalid", preserved: false },
    { name: "raw at sign", value: "unix:path=/tmp/dbus@bad", classification: "invalid", preserved: false },
    { name: "malformed percent", value: "unix:path=/tmp/dbus%", classification: "invalid", preserved: false },
    { name: "raw Unicode", value: "unix:path=/tmp/あ", classification: "invalid", preserved: false },
    { name: "newline", value: "unix:path=/run/user/1000/bus\nTOKEN=secret", classification: "invalid", preserved: false },
  ] as const;

  for (const fixture of fixtures) {
    await t.test(fixture.name, () => {
      const parentEnvironment: NodeJS.ProcessEnv = {
        PATH: "/usr/bin",
        AUTOSTREAM_UNRELATED: "preserved",
      };
      if (fixture.value !== undefined) parentEnvironment.DBUS_SESSION_BUS_ADDRESS = fixture.value;
      const parentSnapshot = { ...parentEnvironment };

      assert.equal(classifyDbusSessionBusAddress(fixture.value), fixture.classification);
      const profile = buildBrowserLaunchProfile({
        platform: "linux",
        attemptNumber: 1,
        userDataDirectory: "/owned/profile",
        xdgRuntimeDirectory: "/owned/xdg-runtime",
        parentEnvironment,
      });
      assert.equal(Object.hasOwn(profile.environment, "DBUS_SESSION_BUS_ADDRESS"), fixture.preserved);
      assert.equal(profile.environment.DBUS_SESSION_BUS_ADDRESS, fixture.preserved ? fixture.value : undefined);
      assert.equal(profile.environment.AUTOSTREAM_UNRELATED, "preserved");
      assert.deepEqual(parentEnvironment, parentSnapshot);

      const facts = collectBrowserLaunchFacts("/configured/google-chrome", {
        platform: "linux",
        environment: parentEnvironment,
        inspectDirectory: () => true,
        resolveExecutableRealpath: () => "/configured/google-chrome",
        readBrowserVersion: () => null,
        readDevShm: () => null,
        executableAvailable: () => false,
      });
      const diagnostic = formatBrowserLaunchFacts(facts);
      assert.equal(facts.dbusSessionBusAddress, fixture.classification);
      if (fixture.value !== undefined) assert.equal(diagnostic.includes(fixture.value), false);
    });
  }
});

test("XDG classification observes directory validity without returning or logging the path", () => {
  const inspected: string[] = [];
  const inspectDirectory = (path: string) => {
    inspected.push(path);
    if (path === "/valid/runtime") return true;
    if (path === "/throws/runtime") throw new Error("private inspection detail");
    return false;
  };

  assert.equal(classifyXdgRuntimeDirectory(undefined, inspectDirectory), "absent");
  assert.equal(classifyXdgRuntimeDirectory("/valid/runtime", inspectDirectory), "valid-directory");
  assert.equal(classifyXdgRuntimeDirectory("/file/runtime", inspectDirectory), "invalid");
  assert.equal(classifyXdgRuntimeDirectory("/throws/runtime", inspectDirectory), "invalid");
  assert.deepEqual(inspected, ["/valid/runtime", "/file/runtime", "/throws/runtime"]);
});

test("launch facts and their formatter are bounded and non-secret", () => {
  const rawDbus = "unix:path=/run/user/1000/private-bus";
  const rawToken = "must-not-appear-in-launch-facts";
  const rawXdgPath = "/run/user/1000/private-runtime";
  const facts = collectBrowserLaunchFacts("/configured/google-chrome", {
    platform: "linux",
    environment: {
      PATH: "/usr/bin",
      XDG_RUNTIME_DIR: rawXdgPath,
      DBUS_SESSION_BUS_ADDRESS: rawDbus,
    },
    inspectDirectory: (path) => path === rawXdgPath,
    resolveExecutableRealpath: () => "/opt/google/chrome/google-chrome",
    readBrowserVersion: () => `Google Chrome 151.0.7922.173 Bearer ${rawToken}${"x".repeat(500)}`,
    readDevShm: () => ({ totalBytes: 67_108_864, availableBytes: 50_331_648 }),
    executableAvailable: (name) => name === "dbus-run-session",
  });
  const diagnostic = formatBrowserLaunchFacts(facts);

  assert.equal(facts.platform, "linux");
  assert.equal(facts.browserExecutableBasename, "google-chrome");
  assert.equal(facts.browserExecutableRealpath, "/opt/google/chrome/google-chrome");
  assert.equal(facts.xdgRuntimeDirectory, "valid-directory");
  assert.equal(facts.dbusSessionBusAddress, "valid-unix");
  assert.deepEqual(facts.devShm, { available: true, totalBytes: 67_108_864, availableBytes: 50_331_648 });
  assert.equal(facts.dbusRunSessionAvailable, true);
  assert.ok(facts.browserVersion.length <= 160, "browser version must be bounded");
  assert.doesNotMatch(facts.browserVersion, new RegExp(rawToken));
  assert.doesNotMatch(diagnostic, new RegExp(rawToken));
  assert.doesNotMatch(diagnostic, new RegExp(rawDbus));
  assert.doesNotMatch(diagnostic, new RegExp(rawXdgPath));
  assert.match(diagnostic, /platform=linux/);
  assert.match(diagnostic, /browserExecutable=google-chrome/);
  assert.match(diagnostic, /browserRealpath="\/opt\/google\/chrome\/google-chrome"/);
  assert.match(diagnostic, /browserVersion=/);
  assert.match(diagnostic, /xdgRuntime=valid-directory/);
  assert.match(diagnostic, /dbusSession=valid-unix/);
  assert.match(diagnostic, /devShmAvailable=yes/);
  assert.match(diagnostic, /devShmTotalBytes=67108864/);
  assert.match(diagnostic, /devShmAvailableBytes=50331648/);
  assert.match(diagnostic, /dbusRunSessionAvailable=yes/);
  assert.ok(diagnostic.length < 1_000, "launch facts must remain bounded");
});

test("diagnostic sanitizer removes credentials, cookies, D-Bus values, controls, and excess output", () => {
  const rawSecret = "diagnostic-secret-value";
  const sanitized = sanitizeBrowserDiagnosticText([
    `\u001b[31m${"x".repeat(3_000)}\u001b[0m`,
    `Authorization: Bearer ${rawSecret}`,
    `Cookie: session=${rawSecret}`,
    `Set-Cookie: session=${rawSecret}`,
    `DBUS_SESSION_BUS_ADDRESS=unix:path=/${rawSecret}`,
    `https://example.test/?access_token=${rawSecret}`,
    `PASSWORD=${rawSecret}`,
    `failed to open "C:\\Users\\runner\\private\\profile.db"`,
    `failed to open "/home/runner/private/profile.db"`,
  ].join("\n"));

  assert.doesNotMatch(sanitized, new RegExp(rawSecret));
  assert.doesNotMatch(sanitized, /C:\\Users\\runner\\private/);
  assert.doesNotMatch(sanitized, /\/home\/runner\/private/);
  assert.doesNotMatch(sanitized, /\u001b/);
  assert.match(sanitized, /<redacted>/);
  assert.ok(sanitized.length <= 2_000);
});
