import assert from "node:assert/strict";
import { register } from "node:module";
import { QueryClient } from "@tanstack/react-query";
import { registerSourceResolution } from "./moved-source.mts";

const source = `let root;export function initialize(data){root=data.root}export async function resolve(specifier,context,next){if(specifier.startsWith('@/')){const url=new URL('src/'+specifier.slice(2),root);if(!/\\.[cm]?[jt]sx?$/.test(url.pathname))url.pathname+='.ts';return next(url.href,context)}return next(specifier,context)}`;
register(`data:text/javascript,${encodeURIComponent(source)}`, { parentURL: import.meta.url, data: { root: new URL("../../", import.meta.url).href } });
registerSourceResolution();

export async function verifyActualCreateAuthority(apiOrigin: string, encoder: string, worker: string) {
  const { streamPermissionSnapshot, streamActionStateSnapshot, revalidateStreamActionAuthority } = await import("../../src/features/streams/stream-action-runtime.ts");
  const originalFetch = globalThis.fetch;
  const client = new QueryClient();
  globalThis.fetch = (input, init) => originalFetch(typeof input === "string" && input.startsWith("/") ? new URL(input, apiOrigin) : input, init);
  try {
    const user = await fetch(`${apiOrigin}/auth/me`).then(response => response.json());
    const services = await fetch(`${apiOrigin}/service-health`).then(response => response.json());
    client.setQueryData(["auth", "me"], user);
    client.setQueryData(["service-health"], services);
    const intent = { id: "STR-01" as const, publicLabel: "SQL browser actual payload", payload: { name: "SQL browser actual payload", auto_start_trigger: "", encoder_service_id: encoder, worker_service_id: worker } };
    const initial = streamActionStateSnapshot(client, intent);
    const proven = await revalidateStreamActionAuthority(client, intent, new AbortController().signal);
    const observation = { permissions: streamPermissionSnapshot(client).kind, initial: initial.kind, proven: proven.state.kind, freshness: proven.state.freshness, matching: initial.fingerprint === proven.state.fingerprint, serviceArray: Array.isArray(services) };
    assert.equal(observation.permissions, "ready");
    assert.equal(initial.kind, "ready");
    assert.equal(proven.state.kind, "ready");
    assert.equal(observation.matching, true);
    return observation;
  } finally { globalThis.fetch = originalFetch; client.clear(); }
}
