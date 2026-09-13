import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { buildBootstrapEnvelopeAAD, encryptBootstrapCredentials } from "../src/lib/bootstrap-envelope.ts";
import { toBase64URL, fromBase64URL } from "./system-updates-fixture.mts";


export function registerBootstrapCryptoCases() {


test("bootstrap envelope uses canonical AAD and P-256 ECDH AES-GCM without plaintext fields", async () => {
  const receiver = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
  const receiverPublicKey = toBase64URL(new Uint8Array(await crypto.subtle.exportKey("raw", receiver.publicKey)));
  const context = {
    updaterID: "updater-main",
    expectedRevision: 7,
    jobID: "019f-bootstrap-job",
    hostIDs: ["host-z", "host-a"],
  };
  const credentials = {
    administrator_user: "autostream-admin",
    private_key: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----",
    passphrase: "one-time-passphrase",
  };

  assert.equal(
    buildBootstrapEnvelopeAAD(context),
    '{"version":1,"updater_id":"updater-main","policy_revision":7,"job_id":"019f-bootstrap-job","host_ids":["host-a","host-z"]}',
  );

  const envelope = await encryptBootstrapCredentials(receiverPublicKey, context, credentials);
  assert.equal(envelope.version, 1);
  assert.doesNotMatch(JSON.stringify(envelope), /autostream-admin|OPENSSH|one-time-passphrase/);

  const ephemeralPublicKey = await crypto.subtle.importKey(
    "raw",
    fromBase64URL(envelope.ephemeral_public_key),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const sharedSecret = await crypto.subtle.deriveBits(
    { name: "ECDH", public: ephemeralPublicKey },
    receiver.privateKey,
    256,
  );
  const hkdfKey = await crypto.subtle.importKey("raw", sharedSecret, "HKDF", false, ["deriveKey"]);
  const contentKey = await crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(0),
      info: new TextEncoder().encode("autostream-bootstrap-envelope-v1"),
    },
    hkdfKey,
    { name: "AES-GCM", length: 256 },
    false,
    ["decrypt"],
  );
  const plaintext = await crypto.subtle.decrypt(
    {
      name: "AES-GCM",
      iv: fromBase64URL(envelope.nonce),
      additionalData: new TextEncoder().encode(buildBootstrapEnvelopeAAD(context)),
    },
    contentKey,
    fromBase64URL(envelope.ciphertext),
  );
  assert.deepEqual(JSON.parse(new TextDecoder().decode(plaintext)), {
    administrator_user: credentials.administrator_user,
    private_key: toBase64URL(new TextEncoder().encode(credentials.private_key)),
    passphrase: toBase64URL(new TextEncoder().encode(credentials.passphrase)),
  });
});

test("bootstrap envelope matches the Go WebCrypto interoperability vector", async () => {
  const recipientPrivate = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE";
  const recipientPublic = fromBase64URL("BGsX0fLhLEJH-Lzm5WOkQPJ3A32BLeszoPShOUXYmMKWT-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU");
  const recipientKey = await crypto.subtle.importKey(
    "jwk",
    {
      kty: "EC",
      crv: "P-256",
      x: toBase64URL(recipientPublic.slice(1, 33)),
      y: toBase64URL(recipientPublic.slice(33, 65)),
      d: recipientPrivate,
      ext: true,
      key_ops: ["deriveBits"],
    },
    { name: "ECDH", namedCurve: "P-256" },
    false,
    ["deriveBits"],
  );
  const ephemeralPublicKey = await crypto.subtle.importKey(
    "raw",
    fromBase64URL("BHzyexiNA09-ilI4AwS1GsPAiWnid_IbNaYLSPxHZpl4B3dVENuO0EApPZrGn3Qw27p9reY86YIpngS3nSJ4c9E"),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const sharedSecret = await crypto.subtle.deriveBits(
    { name: "ECDH", public: ephemeralPublicKey },
    recipientKey,
    256,
  );
  const hkdfKey = await crypto.subtle.importKey("raw", sharedSecret, "HKDF", false, ["deriveKey"]);
  const contentKey = await crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(0),
      info: new TextEncoder().encode("autostream-bootstrap-envelope-v1"),
    },
    hkdfKey,
    { name: "AES-GCM", length: 256 },
    false,
    ["decrypt"],
  );
  const context = {
    updaterID: "updater-01",
    expectedRevision: 7,
    jobID: "bootstrap-job-01",
    hostIDs: ["host-b", "host-a"],
  };
  const plaintext = await crypto.subtle.decrypt(
    {
      name: "AES-GCM",
      iv: fromBase64URL("AAECAwQFBgcICQoL"),
      additionalData: new TextEncoder().encode(buildBootstrapEnvelopeAAD(context)),
    },
    contentKey,
    fromBase64URL("2KTE0tK-dlqNjJhoI4r7bcqaKhpQksriceJVF6BZYOGFQfKoOJEiSzNIJVCzxYmwMLD9ozGPidtYQA9R1aOJndP3rJQ4ViWbW8wc4KIWmG6iPwe6nQsATMHRef1y2XFVuY5KmQM3d2etdodnItFv8vZAsuXEgSgaje8"),
  );

  assert.equal(
    buildBootstrapEnvelopeAAD(context),
    '{"version":1,"updater_id":"updater-01","policy_revision":7,"job_id":"bootstrap-job-01","host_ids":["host-a","host-b"]}',
  );
  assert.equal(
    new TextDecoder().decode(plaintext),
    '{"administrator_user":"deploy","private_key":"dGVzdC1wcml2YXRlLWtleQ","passphrase":"dGVzdC1wYXNzcGhyYXNl"}',
  );
});

test("bootstrap ephemeral ECDH private key is not extractable while the public raw key remains exportable", async () => {
  const keys = await crypto.subtle.generateKey(
    { name: "ECDH", namedCurve: "P-256" },
    false,
    ["deriveBits"],
  );
  assert.equal(keys.privateKey.extractable, false);
  assert.equal(keys.publicKey.extractable, true);
  await assert.rejects(() => crypto.subtle.exportKey("pkcs8", keys.privateKey));
  assert.equal((await crypto.subtle.exportKey("raw", keys.publicKey)).byteLength, 65);

  const source = readFileSync(new URL("../src/lib/bootstrap-envelope.ts", import.meta.url), "utf8");
  assert.match(source, /generateKey\([\s\S]*?\},\s*false,\s*\["deriveBits"\]/);
});
}
