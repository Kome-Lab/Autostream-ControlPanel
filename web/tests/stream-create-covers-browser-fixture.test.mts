import assert from "node:assert/strict";
import test from "node:test";
import { fixtureRoutes } from "./stream-create-covers-browser-fixture.mts";

const operationID = "b6cf8101-80f4-4d57-9610-f1a3d41aa245";
function multipart(fields: Record<string, string>) {
  const boundary = "------fixture-boundary";
  return Object.entries(fields).map(([name, value]) => `${boundary}\r\nContent-Disposition: form-data; name="${name}"\r\n\r\n${value}\r\n`).join("")
    + `${boundary}\r\nContent-Disposition: form-data; name="file"; filename="cover.png"\r\nContent-Type: image/png\r\n\r\nfixture-bytes\r\n${boundary}--\r\n`;
}
function setup() {
  const app = fixtureRoutes();
  const send = (method: string, path: string, postData?: string) => app.route({ method, url: `http://fixture.local${path}`, postData });
  const session = send("POST", "/media-assets/upload-sessions")!.body as { id: string };
  return { ...app, send, session, fields: { session_id: session.id, usage_type: "video_cover", client_operation_id: operationID } };
}

test("browser upload fixture echoes the actual session, usage and nonce and reuses that operation", () => {
  const flow = setup();
  const empty = flow.send("GET", `/media-assets/upload-sessions/${flow.session.id}/assets?operation_id=${operationID}`)!.body as { assets: unknown[] };
  assert.deepEqual(empty.assets, []);
  const asset = flow.send("POST", "/media-assets", multipart({ ...flow.fields, usage_type: "scene_background" }))!.body as { id: string; client_operation_id: string; owner_id: string; usage_type: string; sha256: string };
  assert.equal(asset.client_operation_id, operationID); assert.equal(asset.owner_id, flow.session.id); assert.equal(asset.usage_type, "scene_background"); assert.match(asset.sha256, /^[a-f0-9]{64}$/);
  const again = flow.send("POST", "/media-assets", multipart({ ...flow.fields, usage_type: "scene_background" }))!.body;
  assert.deepEqual(again, asset); assert.equal(flow.state.uploads, 1);
  const variant = flow.send("POST", `/media-assets/${asset.id}/variants`, JSON.stringify({ width: 1920, height: 1080, opaque: true }))!.body;
  const lookup = flow.send("GET", `/media-assets/upload-sessions/${flow.session.id}/assets?operation_id=${operationID}`)!.body as { assets: { asset: unknown; variants: unknown[] }[] };
  assert.deepEqual(lookup.assets, [{ asset, variants: [variant] }]);
});

test("fixture requires transmitted metadata and cannot invent an absent nonce or a foreign session", () => {
  const flow = setup();
  assert.throws(() => flow.send("POST", "/media-assets"), /actual multipart metadata/);
  assert.throws(() => flow.send("POST", "/media-assets", multipart({ session_id: flow.session.id, usage_type: "video_cover" })), /actual operation nonce/);
  assert.equal(flow.send("POST", "/media-assets", multipart({ ...flow.fields, session_id: "foreign-session" }))!.status, 404);
  assert.equal(flow.send("GET", `/media-assets/upload-sessions/foreign-session/assets?operation_id=${operationID}`)!.status, 404);
  assert.equal(flow.send("GET", `/media-assets/upload-sessions/${flow.session.id}/assets?operation_id=invalid`)!.status, 400);
  assert.equal(flow.state.uploads, 0);
});

test("exact-session deletion leaves a tombstone and rejects nonce replay and claimed cleanup", () => {
  const flow = setup(), asset = flow.send("POST", "/media-assets", multipart(flow.fields))!.body as { id: string };
  assert.equal(flow.send("DELETE", `/media-assets/${asset.id}?expected_upload_session_id=foreign`)!.status, 403);
  assert.equal(flow.send("DELETE", `/media-assets/${asset.id}?expected_upload_session_id=${flow.session.id}`)!.status, 204);
  const result = flow.send("GET", `/media-assets/upload-sessions/${flow.session.id}/assets?operation_id=${operationID}`)!.body as { assets: { asset: { deleted_at?: string } }[] };
  assert.ok(result.assets[0].asset.deleted_at);
  assert.equal(flow.send("POST", "/media-assets", multipart(flow.fields))!.status, 404);
  const nextOperation = "00000000-0000-4000-8000-000000000001";
  const live = flow.send("POST", "/media-assets", multipart({ ...flow.fields, client_operation_id: nextOperation }))!.body as { id: string };
  flow.send("POST", "/streams", JSON.stringify({ name: "Saved", upload_session_id: flow.session.id, visual_settings: { cover_candidates: [] } }));
  assert.equal(flow.send("DELETE", `/media-assets/${live.id}?expected_upload_session_id=${flow.session.id}`)!.status, 409);
  const claimed = flow.send("GET", `/media-assets/upload-sessions/${flow.session.id}/assets?operation_id=${nextOperation}`)!.body as { session: { claimed_stream_id?: string }; assets: { asset: { owner_type: string } }[] };
  assert.ok(claimed.session.claimed_stream_id); assert.equal(claimed.assets[0].asset.owner_type, "stream");
});
