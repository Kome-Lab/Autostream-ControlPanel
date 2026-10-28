import { register } from "node:module";
import type { TestContext } from "node:test";
import { QueryClient } from "@tanstack/react-query";
import { registerSourceResolution } from "./moved-source.mts";
import type { ResourceActionIntent } from "../../src/features/resources/resource-action-descriptors.ts";

registerSourceResolution();
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
  data: { webRootURL: new URL("../../", import.meta.url).href },
});

const { createResourceActionController } = await import("../../src/features/resources/resource-action-controller.ts");
const { apiGet } = await import("../../src/lib/api/client.ts");
export const {
  mutateResourceAction, refreshResourceAction, revalidateResourceAction,
  resourceActionStateSnapshot, resourcePermissionSnapshot, resourceSecurityMetadata,
} = await import("../../src/features/resources/resource-action-runtime.ts");

export const oldRow = () => ({ id: "profile-one", name: "Existing BOT", updated_at: "2026-10-28T00:00:00Z" });
export const createIntent = (): ResourceActionIntent => ({
  id: "RES-04", payload: { name: "Draft BOT", service_id: "bot-one" }, publicLabel: "Draft BOT",
});

type Hold = ReturnType<typeof requestHold>;

export function runtimeFixture(t: TestContext, path = "/discord/configs", initialData?: Record<string, unknown>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  let rows: Record<string, unknown>[] = [oldRow()];
  let singleton = initialData;
  let permissions: string[] = ["*"];
  let rejection: { status: number; code: string } | undefined;
  let lost = false, nextRead: Hold | undefined, nextWrite: Hold | undefined;
  const holds: Hold[] = [];
  const writes: { method: string; path: string; body: unknown }[] = [];
  const demo = process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
  const currentData = () => singleton ?? { items: rows };
  process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = "false";
  client.setQueryData(["auth", "me"], { permissions });
  client.setQueryData(["resource", path], currentData());
  t.after(() => {
    controller.cancel();
    for (const hold of holds) hold.release();
    client.clear();
    if (demo === undefined) delete process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO;
    else process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO = demo;
  });
  t.mock.method(globalThis, "fetch", async (input: string | URL | Request, init?: RequestInit) => {
    const target = String(input), method = init?.method || "GET";
    if (method === "GET" && target === "/auth/me") return Response.json({ permissions });
    if (method === "GET" && target === path) {
      const snapshot = structuredClone(currentData()), hold = nextRead;
      nextRead = undefined;
      if (hold) await hold.wait(init?.signal);
      return Response.json(snapshot);
    }
    if ((method === "POST" && target === path) || (method === "PUT" && (target === path || target.startsWith(path + "/"))) || (method === "DELETE" && target.startsWith(path + "/"))) {
      writes.push({ method, path: target, body: init?.body === undefined ? undefined : JSON.parse(String(init.body)) });
      const hold = nextWrite; nextWrite = undefined;
      if (hold) await hold.wait(init?.signal);
      if (lost) { lost = false; throw new TypeError("controlled response loss"); }
      if (rejection) {
        const value = rejection; rejection = undefined;
        return Response.json({ code: value.code }, { status: value.status });
      }
      if (method === "DELETE") rows = rows.filter((row) => target !== `${path}/${encodeURIComponent(String(row.id))}`);
      return Response.json({ id: "created-one" }, { status: method === "POST" ? 201 : 200 });
    }
    throw new Error(`Unexpected controlled request: ${method} ${target}`);
  });
  const controller = createResourceActionController({
    getPermissions: () => resourcePermissionSnapshot(client),
    getState: (intent) => resourceActionStateSnapshot(client, intent),
    refresh: (intent, signal) => refreshResourceAction(client, intent, signal),
    revalidate: (intent, signal) => revalidateResourceAction(client, intent, signal),
    mutate: (request) => mutateResourceAction(request, client),
  });
  return {
    client, path, controller, writes,
    setRows: (value: Record<string, unknown>[]) => { rows = value; },
    setData: (value: Record<string, unknown>) => { singleton = value; },
    setPermissions: (value: string[]) => { permissions = value; },
    readManagedResource: () => client.fetchQuery({ queryKey: ["resource", path], queryFn: ({ signal }) => apiGet<unknown>(path, signal) }),
    rejectNext: (status: number, code: string) => { rejection = { status, code }; },
    loseNextResponse: () => { lost = true; },
    holdNextRead: () => { const hold = requestHold(); holds.push(hold); nextRead = hold; return hold; },
    holdNextWriteResponse: () => { const hold = requestHold(); holds.push(hold); nextWrite = hold; return hold; },
  };
}

export function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => { resolve = complete; });
  return { promise, resolve };
}

function requestHold() {
  const started = deferred<void>(), released = deferred<void>();
  let signal: AbortSignal | null | undefined;
  return {
    started: started.promise,
    release: () => released.resolve(),
    get signal() { return signal; },
    wait: async (value: AbortSignal | null | undefined) => { signal = value; started.resolve(); await released.promise; },
  };
}
