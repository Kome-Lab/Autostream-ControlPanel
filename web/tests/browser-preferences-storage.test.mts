import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import { runInNewContext } from "node:vm";
import * as React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ts from "typescript";
import * as preferences from "../src/features/account/ui-preferences.ts";
import * as i18n from "../src/lib/i18n.ts";
import type { UserUIPreference } from "../src/features/account/ui-preferences.ts";

const require = createRequire(import.meta.url);
const themeSource = readFileSync(new URL("../src/components/admin/theme-provider.tsx", import.meta.url), "utf8");
const i18nSource = readFileSync(new URL("../src/components/admin/i18n-provider.tsx", import.meta.url), "utf8");
type ThemeModule = typeof import("../src/components/admin/theme-provider.tsx");
type I18nModule = typeof import("../src/components/admin/i18n-provider.tsx");
type Fault = "getter" | "getItem" | "setItem" | null;
type QueryOptions = { queryKey: readonly string[]; queryFn: () => Promise<UserUIPreference>; retry: boolean; enabled: boolean };
const defaultTheme = { theme_id: "autostream", color_mode: "system", revision: 0 };
const dbTheme = Object.freeze({ theme_id: "violet", color_mode: "light", revision: 42, updated_at: "2026-09-11T00:00:00Z" });

function storageError(name = "SecurityError") {
  return Object.assign(new Error("SYNTHETIC_STORAGE_ERROR_DO_NOT_LOG"), { name });
}

// Load the complete production TSX, as in the logout regression. Only React,
// router/query and browser boundaries are controlled; preference/i18n code is real.
function loadModule<T>(source: string, imports: Record<string, unknown>, globals: Record<string, unknown> = {}): T {
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports: Record<string, unknown> = {};
  runInNewContext(output, {
    ...globals, exports,
    require: (name: string) => {
      if (name === "react/jsx-runtime") return require(name);
      assert.ok(Object.hasOwn(imports, name), `unexpected production import: ${name}`);
      return imports[name];
    },
  });
  return exports as T;
}

function hookHost(hydrated: { value: boolean }) {
  type Cell = { initialized?: boolean; value?: unknown; deps?: readonly unknown[]; cleanup?: () => void };
  const cells: Cell[] = [];
  const contexts = new Map<unknown, unknown>();
  const effects: Array<() => void> = [];
  let cursor = 0;
  let dirty = false;
  const cell = () => cells[cursor] ?? (cells[cursor] = {});
  function nextCell() { const current = cell(); cursor++; return current; }
  function changed(current: Cell, deps: readonly unknown[]) {
    return !current.initialized || current.deps?.length !== deps.length || deps.some((value, index) => !Object.is(value, current.deps?.[index]));
  }
  function memoCell<T>(factory: () => T, deps: readonly unknown[]) {
    const current = nextCell();
    if (changed(current, deps)) {
      current.value = factory(); current.deps = [...deps]; current.initialized = true;
    }
    return current.value as T;
  }
  const hooks = {
    createContext: React.createContext,
    useContext: <T,>(context: React.Context<T>) => contexts.get(context.Provider) as T,
    useMemo: memoCell,
    useCallback: <T,>(callback: T, deps: readonly unknown[]) => memoCell(() => callback, deps),
    useState: <T,>(initial: T | (() => T)) => {
      const current = nextCell();
      if (!current.initialized) {
        current.value = typeof initial === "function" ? (initial as () => T)() : initial;
        current.initialized = true;
      }
      return [current.value as T, (next: T | ((previous: T) => T)) => {
        const value = typeof next === "function" ? (next as (previous: T) => T)(current.value as T) : next;
        if (!Object.is(value, current.value)) { current.value = value; dirty = true; }
      }] as const;
    },
    useEffect: (effect: () => void | (() => void), deps: readonly unknown[]) => {
      const current = nextCell();
      if (changed(current, deps)) {
        current.deps = [...deps]; current.initialized = true;
        effects.push(() => {
          current.cleanup?.();
          const cleanup = effect();
          current.cleanup = typeof cleanup === "function" ? cleanup : undefined;
        });
      }
    },
    useSyncExternalStore: (subscribe: unknown, client: () => boolean, server: () => boolean) => {
      void subscribe; nextCell();
      return hydrated.value ? client() : server();
    },
  };
  return {
    hooks,
    render(component: () => React.ReactElement) {
      cursor = 0; dirty = false;
      const element = component() as React.ReactElement<{ value: unknown; children: React.ReactNode }>;
      contexts.set(element.type, element.props.value);
      return element;
    },
    flushEffects() { for (const effect of effects.splice(0)) effect(); },
    get dirty() { return dirty; },
    close() { for (const current of cells) current.cleanup?.(); },
  };
}

function createProviders(options: { fault?: Fault; errorName?: string; data?: UserUIPreference; pathname?: string; hydrated?: boolean; mirror?: string | null; locale?: string | null } = {}) {
  const values = new Map<string, string>([["autostream.theme", "SYNTHETIC_LEGACY_VALUE_DO_NOT_USE"]]);
  if (options.mirror !== null) values.set(preferences.themeMirrorStorageKey, options.mirror ?? JSON.stringify({ theme_id: "ocean", color_mode: "system" }));
  if (options.locale !== null) values.set(i18n.localeStorageKey, options.locale ?? "ja");
  const reads: string[] = [], writes: Array<{ key: string; value: string }> = [];
  const forbidden: string[] = [], logs: unknown[][] = [], apiCalls: string[] = [];
  const listeners = new Set<() => void>();
  const hydrated = { value: options.hydrated ?? true };
  const classes = new Set<string>();
  let data = options.data, fault = options.fault ?? null;
  let getterCalls = 0, mediaAdds = 0, mediaRemoves = 0, explicitQueries = 0;
  let queryError: Error | undefined, domError: Error | undefined, lang = "";
  let queryOptions!: QueryOptions;
  const error = storageError(options.errorName);
  const deny = (name: string) => { forbidden.push(name); throw new Error("unexpected boundary: " + name); };
  const storage = {
    getItem(key: string) { reads.push(key); if (fault === "getItem") throw error; return values.get(key) ?? null; },
    setItem(key: string, value: string) { writes.push({ key, value }); if (fault === "setItem") throw error; values.set(key, value); },
    removeItem: () => deny("removeItem"), clear: () => deny("clear"),
  };
  const media = {
    matches: false,
    addEventListener(name: string, callback: () => void) { assert.equal(name, "change"); mediaAdds++; listeners.add(callback); },
    removeEventListener(name: string, callback: () => void) { assert.equal(name, "change"); mediaRemoves++; listeners.delete(callback); },
  };
  const root = {
    dataset: {} as Record<string, string>,
    classList: { toggle(name: string, enabled: boolean) { if (enabled) classes.add(name); else classes.delete(name); return enabled; } },
    get lang() { return lang; }, set lang(value: string) { if (domError) throw domError; lang = value; },
  };
  const window = {
    get localStorage() { getterCalls++; if (fault === "getter") throw error; return storage; },
    get sessionStorage() { return deny("sessionStorage"); },
    matchMedia(query: string) { assert.equal(query, "(prefers-color-scheme: dark)"); return media; },
    setTimeout: () => deny("window.setTimeout"), fetch: () => deny("window.fetch"),
  };
  const document = { documentElement: root, get cookie() { return deny("cookie"); }, set cookie(value: string) { void value; deny("cookie"); } };
  const globals = {
    window, document, console: Object.fromEntries(["log", "error", "warn", "info", "debug"].map(name => [name, (...args: unknown[]) => logs.push(args)])),
    setTimeout: () => deny("setTimeout"), fetch: () => deny("fetch"),
  };
  const themeHost = hookHost(hydrated), languageHost = hookHost(hydrated);
  const theme = loadModule<ThemeModule>(themeSource, {
    react: themeHost.hooks, "next/navigation": { usePathname: () => options.pathname ?? "/admin/account" },
    "@tanstack/react-query": { useQuery: (next: QueryOptions) => {
      queryOptions = next;
      assert.deepEqual([...next.queryKey], [...preferences.uiPreferenceQueryKey]); assert.equal(next.retry, false);
      if (queryError) throw queryError;
      return { data, isError: false };
    } },
    "@/lib/api/client": { apiGet: async (url: string) => { apiCalls.push(url); if (queryError) throw queryError; return data; }, apiPut: () => deny("apiPut"), apiPost: () => deny("apiPost") },
    "@/features/account/ui-preferences": preferences,
  }, globals);
  const language = loadModule<I18nModule>(i18nSource, { react: languageHost.hooks, "@/lib/i18n": i18n }, globals);
  const child = React.createElement("span", null, "provider-child-sentinel");
  function render() {
    const nested = React.createElement(language.I18nProvider, null, child);
    const outer = themeHost.render(() => theme.ThemeProvider({ children: nested }));
    assert.equal(outer.props.children, nested, "real ThemeProvider must return its child tree");
    const inner = languageHost.render(() => language.I18nProvider(nested.props));
    assert.equal(inner.props.children, child, "real I18nProvider must return its child tree");
  }
  function commit() {
    for (let cycle = 0; cycle < 5; cycle++) {
      render(); themeHost.flushEffects(); languageHost.flushEffects();
      if (!themeHost.dirty && !languageHost.dirty) return;
    }
    assert.fail("Provider effects did not settle");
  }
  return {
    commit, values, reads, writes, root, classes, listeners, apiCalls, hydrated,
    get theme() { return theme.useTheme(); }, get language() { return language.useI18n(); },
    get getterCalls() { return getterCalls; }, get mediaAdds() { return mediaAdds; }, get mediaRemoves() { return mediaRemoves; },
    get queryOptions() { return queryOptions; },
    setData(next: UserUIPreference) { data = next; }, setFault(next: Fault) { fault = next; },
    failQuery(next: Error) { queryError = next; }, failDocument(next: Error) { domError = next; },
    requestPreference() { explicitQueries++; return queryOptions.queryFn(); },
    systemDark(next: boolean) { media.matches = next; for (const callback of listeners) callback(); },
    close() {
      themeHost.close(); languageHost.close();
      assert.equal(listeners.size, 0); assert.deepEqual(forbidden, []); assert.deepEqual(logs, []);
      assert.equal(apiCalls.length, explicitQueries, "Storage failures must not cause API requests or retries");
      for (const key of [...reads, ...writes.map(write => write.key)]) assert.ok([preferences.themeMirrorStorageKey, i18n.localeStorageKey].includes(key));
      assert.equal(values.get("autostream.theme"), "SYNTHETIC_LEGACY_VALUE_DO_NOT_USE");
    },
  };
}

for (const name of ["SecurityError", "Error"]) {
  test(`production mirror read defaults on ${name} from getItem`, () => {
    assert.deepEqual(preferences.readThemeMirror({ getItem() { throw storageError(name); } }), preferences.safeUserUIPreference(defaultTheme));
  });
}

for (const name of ["QuotaExceededError", "SecurityError"]) {
  test(`production mirror write contains ${name} without changing DB preference`, () => {
    const preference = Object.freeze(preferences.safeUserUIPreference(dbTheme));
    assert.doesNotThrow(() => preferences.writeThemeMirror({ setItem() { throw storageError(name); } }, preference));
    assert.equal(preference.theme_id, "violet"); assert.equal(preference.revision, 42);
  });
}

test("mirror reads retain defaults for absent/corrupt data and writes contain only the two v2 fields", () => {
  for (const raw of [null, "", "{SYNTHETIC_BROKEN_MIRROR", "null"]) {
    const result = preferences.readThemeMirror({ getItem: () => raw });
    assert.equal(result.theme_id, "autostream"); assert.equal(result.color_mode, "system"); assert.equal(result.revision, 0);
  }
  const writes: Array<{ key: string; value: string }> = [];
  preferences.writeThemeMirror({ setItem: (key, value) => writes.push({ key, value }) }, preferences.safeUserUIPreference(dbTheme));
  assert.equal(writes.length, 1); assert.equal(writes[0].key, preferences.themeMirrorStorageKey);
  assert.deepEqual(JSON.parse(writes[0].value), { theme_id: "violet", color_mode: "light" });
});

test("real React SSR renders both production Providers without touching window, Storage or effects", () => {
  const unexpected = () => { assert.fail("SSR accessed a browser or API boundary"); };
  const globals = { document: new Proxy({}, { get: unexpected }), localStorage: new Proxy({}, { get: unexpected }) };
  const theme = loadModule<ThemeModule>(themeSource, {
    react: React, "next/navigation": { usePathname: () => "/admin/account" },
    "@tanstack/react-query": { useQuery: (options: QueryOptions) => { assert.equal(options.enabled, false); assert.equal(options.retry, false); return {}; } },
    "@/lib/api/client": { apiGet: unexpected }, "@/features/account/ui-preferences": preferences,
  }, globals);
  const language = loadModule<I18nModule>(i18nSource, { react: React, "@/lib/i18n": i18n }, globals);
  function Child() {
    assert.equal(theme.useTheme().themeID, "autostream"); assert.equal(theme.useTheme().preference.revision, 0);
    assert.equal(language.useI18n().locale, "ja"); assert.equal(language.useI18n().t("dashboard"), "ダッシュボード");
    return React.createElement("span", null, "SSR-child");
  }
  assert.equal(renderToStaticMarkup(React.createElement(theme.ThemeProvider, null, React.createElement(language.I18nProvider, null, React.createElement(Child)))), "<span>SSR-child</span>");
});

for (const fault of ["getter", "getItem"] as const) {
  test(`both production Providers survive ${fault} failure, then retain arriving DB data`, (t) => {
    const providers = createProviders({ fault }); t.after(() => providers.close());
    assert.doesNotThrow(() => providers.commit());
    assert.equal(providers.theme.themeID, "autostream"); assert.equal(providers.theme.colorMode, "system"); assert.equal(providers.theme.preference.revision, 0);
    assert.equal(providers.language.locale, "ja"); assert.equal(providers.root.lang, "ja"); assert.equal(providers.writes.length, 0);
    providers.setData(dbTheme); assert.doesNotThrow(() => providers.commit());
    assert.equal(providers.theme.themeID, "violet"); assert.equal(providers.theme.preference.revision, 42); assert.equal(providers.root.dataset.theme, "violet");
    assert.doesNotThrow(() => providers.language.setLocale("en")); assert.equal(providers.root.lang, "en"); providers.commit();
    assert.equal(providers.language.locale, "en"); assert.equal(providers.language.t("dashboard"), "Dashboard");
    assert.equal(providers.theme.preference.revision, 42);
    const writes = providers.writes.length, getters = providers.getterCalls; providers.commit();
    assert.equal(providers.writes.length, writes); assert.equal(providers.getterCalls, getters, "stable renders do not retry Storage");
  });
}

for (const name of ["QuotaExceededError", "SecurityError", "Error"]) {
  test(`Provider effects and language handlers survive ${name} writes without reverting DB or locale`, (t) => {
    const providers = createProviders({ fault: "setItem", errorName: name, data: dbTheme }); t.after(() => providers.close());
    assert.doesNotThrow(() => providers.commit());
    assert.equal(providers.theme.themeID, "violet"); assert.equal(providers.theme.preference.revision, 42); assert.equal(providers.root.dataset.colorMode, "light");
    assert.equal(providers.writes.length, 1, "the actual mirror effect executed");
    for (const locale of ["en", "ja"] as const) {
      assert.doesNotThrow(() => providers.language.setLocale(locale)); assert.equal(providers.root.lang, locale, "document lang changes even before effects flush");
      providers.commit(); assert.equal(providers.language.locale, locale); assert.equal(providers.language.t("dashboard"), locale === "en" ? "Dashboard" : "ダッシュボード");
      assert.equal(providers.theme.preference.revision, 42); assert.equal(providers.theme.themeID, "violet");
    }
    assert.equal(providers.values.get(i18n.localeStorageKey), "ja", "failed persistence does not claim a changed stored value");
    assert.deepEqual(JSON.parse(providers.values.get(preferences.themeMirrorStorageKey)!), { theme_id: "ocean", color_mode: "system" });
    const attempts = providers.writes.length; providers.commit(); assert.equal(providers.writes.length, attempts);
  });
}

test("normal mirror and en locale initialize both Providers and persist only supported keys", (t) => {
  const providers = createProviders({ pathname: "/login", locale: "en" }); t.after(() => providers.close()); providers.commit();
  assert.equal(providers.theme.themeID, "ocean"); assert.equal(providers.theme.preference.revision, 0);
  assert.equal(providers.language.locale, "en"); assert.equal(providers.root.lang, "en"); assert.equal(providers.language.t("dashboard"), "Dashboard");
  assert.equal(providers.queryOptions.enabled, false);
  assert.deepEqual(JSON.parse(providers.values.get(preferences.themeMirrorStorageKey)!), { theme_id: "ocean", color_mode: "system" });
  providers.language.setLocale("ja"); providers.commit(); assert.equal(providers.values.get(i18n.localeStorageKey), "ja");
});

test("hydration, DB arrival, unsaved preview and persisted revision retain their existing priority", (t) => {
  const providers = createProviders({ hydrated: false, locale: "unsupported-locale" }); t.after(() => providers.close()); providers.commit();
  assert.equal(providers.theme.themeID, "autostream"); assert.equal(providers.language.locale, "ja");
  assert.deepEqual(providers.reads, [i18n.localeStorageKey]); assert.equal(providers.writes.length, 0);
  providers.hydrated.value = true; providers.commit(); assert.equal(providers.theme.themeID, "ocean"); assert.equal(providers.writes.length, 0);
  providers.theme.previewPreference({ theme_id: "rose" }); providers.commit(); assert.equal(providers.theme.themeID, "rose"); assert.equal(providers.writes.length, 0);
  providers.setData(dbTheme); providers.theme.applyPersistedPreference(dbTheme); providers.commit();
  assert.equal(providers.theme.themeID, "violet"); assert.equal(providers.theme.preference.revision, 42);
  providers.theme.previewPreference({ theme_id: "amber", color_mode: "dark" }); providers.commit();
  assert.equal(providers.theme.themeID, "amber"); assert.equal(providers.root.dataset.theme, "amber");
  assert.deepEqual(JSON.parse(providers.values.get(preferences.themeMirrorStorageKey)!), { theme_id: "violet", color_mode: "light" });
  providers.setFault("setItem"); const saved = Object.freeze({ theme_id: "emerald", color_mode: "system", revision: 43 });
  providers.setData(saved); providers.theme.applyPersistedPreference(saved); assert.doesNotThrow(() => providers.commit());
  assert.equal(providers.theme.themeID, "emerald"); assert.equal(providers.theme.preference.revision, 43); assert.equal(providers.root.dataset.theme, "emerald");
});

test("system theme effect still observes changes and removes its listener", () => {
  const providers = createProviders({ data: { ...dbTheme, color_mode: "system" } });
  try {
    providers.commit(); assert.equal(providers.mediaAdds, 1); assert.equal(providers.classes.has("dark"), false);
    providers.systemDark(true); providers.commit(); assert.equal(providers.theme.dark, true); assert.equal(providers.classes.has("dark"), true);
    providers.systemDark(false); providers.commit(); assert.equal(providers.theme.dark, false); assert.equal(providers.mediaAdds, 1);
  } finally { providers.close(); }
  assert.equal(providers.mediaRemoves, 1);
});

test("Storage protection does not swallow API rejection or query/revision errors", async (t) => {
  const providers = createProviders({ fault: "getter", data: dbTheme }); t.after(() => providers.close()); providers.commit();
  const failure = Object.assign(new Error("SYNTHETIC_API_REVISION_ERROR"), { status: 409, code: "revision_conflict" });
  providers.failQuery(failure);
  await assert.rejects(providers.requestPreference(), error => error === failure);
  assert.deepEqual(providers.apiCalls, ["/account/preferences/ui"]); assert.equal(providers.queryOptions.retry, false);
  assert.throws(() => providers.commit(), error => error === failure);
});

test("locale handler keeps document errors outside the optional Storage catch", (t) => {
  const providers = createProviders({ fault: "setItem", data: dbTheme }); t.after(() => providers.close()); providers.commit();
  const failure = new Error("SYNTHETIC_DOCUMENT_ERROR"); providers.failDocument(failure);
  assert.throws(() => providers.language.setLocale("en"), error => error === failure);
});

test("theme serialization errors are not treated as optional Storage failures", () => {
  const failure = new Error("SYNTHETIC_PREFERENCE_ERROR");
  const invalid = Object.defineProperty(preferences.safeUserUIPreference(dbTheme), "theme_id", { get() { throw failure; } });
  assert.throws(() => preferences.writeThemeMirror({ setItem() { assert.fail("invalid preference reached Storage"); } }, invalid), error => error === failure);
});
