import type { UpdaterHostBootstrapEnvelope } from "@/types/domain";

const bootstrapIdentifierPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const bootstrapHostIDPattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const bootstrapSSHUserPattern = /^[a-z_][a-z0-9_-]{0,31}$/;
const bootstrapEnvelopeMaximumBytes = 64 * 1024;

export type BootstrapEnvelopeContext = {
  updaterID: string;
  expectedRevision: number;
  jobID: string;
  hostIDs: string[];
};

export type BootstrapCredentials = {
  administrator_user: string;
  private_key: string;
  passphrase: string;
};

export function canonicalBootstrapHostIDs(hostIDs: string[]) {
  const normalized = hostIDs.map((hostID) => String(hostID || ""));
  if (
    normalized.length === 0
    || normalized.length > 128
    || normalized.some((hostID) => hostID !== hostID.trim() || !bootstrapHostIDPattern.test(hostID))
  ) {
    throw new Error("bootstrap_host_ids_invalid");
  }
  if (new Set(normalized).size !== normalized.length) {
    throw new Error("bootstrap_host_ids_duplicate");
  }
  return normalized.sort();
}

export function buildBootstrapEnvelopeAAD(context: BootstrapEnvelopeContext) {
  const updaterID = String(context.updaterID || "");
  const jobID = String(context.jobID || "");
  if (
    updaterID !== updaterID.trim()
    || jobID !== jobID.trim()
    || !bootstrapIdentifierPattern.test(updaterID)
    || !bootstrapIdentifierPattern.test(jobID)
    || !Number.isSafeInteger(context.expectedRevision)
    || context.expectedRevision <= 0
  ) {
    throw new Error("bootstrap_envelope_context_invalid");
  }
  return JSON.stringify({
    version: 1,
    updater_id: updaterID,
    policy_revision: context.expectedRevision,
    job_id: jobID,
    host_ids: canonicalBootstrapHostIDs(context.hostIDs),
  });
}

export async function encryptBootstrapCredentials(
  receiverPublicKey: string,
  context: BootstrapEnvelopeContext,
  credentials: BootstrapCredentials,
): Promise<UpdaterHostBootstrapEnvelope> {
  if (!globalThis.crypto?.subtle || typeof globalThis.crypto.getRandomValues !== "function") {
    throw new Error("bootstrap_webcrypto_unavailable");
  }
  const administratorUser = String(credentials.administrator_user || "");
  if (
    administratorUser !== administratorUser.trim()
    || !bootstrapSSHUserPattern.test(administratorUser)
    || administratorUser === "root"
    || !credentials.private_key
  ) {
    throw new Error("bootstrap_credentials_invalid");
  }

  let receiverKey: CryptoKey;
  try {
    const rawReceiverKey = decodeBase64URL(receiverPublicKey);
    if (rawReceiverKey.length !== 65 || rawReceiverKey[0] !== 4) {
      throw new Error("invalid raw P-256 key");
    }
    receiverKey = await globalThis.crypto.subtle.importKey(
      "raw",
      rawReceiverKey,
      { name: "ECDH", namedCurve: "P-256" },
      false,
      [],
    );
  } catch {
    throw new Error("bootstrap_encryption_public_key_invalid");
  }

  const ephemeralKeys = await globalThis.crypto.subtle.generateKey(
    { name: "ECDH", namedCurve: "P-256" },
    false,
    ["deriveBits"],
  );
  const sharedSecret = await globalThis.crypto.subtle.deriveBits(
    { name: "ECDH", public: receiverKey },
    ephemeralKeys.privateKey,
    256,
  );
  const sharedSecretBytes = new Uint8Array(sharedSecret);
  let hkdfKey: CryptoKey;
  try {
    hkdfKey = await globalThis.crypto.subtle.importKey(
      "raw",
      sharedSecretBytes,
      "HKDF",
      false,
      ["deriveKey"],
    );
  } finally {
    sharedSecretBytes.fill(0);
  }
  const contentKey = await globalThis.crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(0),
      info: new TextEncoder().encode("autostream-bootstrap-envelope-v1"),
    },
    hkdfKey,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt"],
  );
  const nonce = globalThis.crypto.getRandomValues(new Uint8Array(12));
  const aad = new TextEncoder().encode(buildBootstrapEnvelopeAAD(context));
  const plaintext = new TextEncoder().encode(JSON.stringify({
    administrator_user: administratorUser,
    private_key: encodeTextBase64URL(credentials.private_key),
    passphrase: encodeTextBase64URL(credentials.passphrase || ""),
  }));

  try {
    const ciphertext = await globalThis.crypto.subtle.encrypt(
      { name: "AES-GCM", iv: nonce, additionalData: aad },
      contentKey,
      plaintext,
    );
    const ephemeralPublicKey = await globalThis.crypto.subtle.exportKey("raw", ephemeralKeys.publicKey);
    const envelope: UpdaterHostBootstrapEnvelope = {
      version: 1,
      ephemeral_public_key: encodeBase64URL(new Uint8Array(ephemeralPublicKey)),
      nonce: encodeBase64URL(nonce),
      ciphertext: encodeBase64URL(new Uint8Array(ciphertext)),
    };
    if (new TextEncoder().encode(JSON.stringify(envelope)).byteLength > bootstrapEnvelopeMaximumBytes) {
      throw new Error("bootstrap_envelope_too_large");
    }
    return envelope;
  } finally {
    plaintext.fill(0);
  }
}

function encodeTextBase64URL(value: string) {
  const bytes = new TextEncoder().encode(value);
  try {
    return encodeBase64URL(bytes);
  } finally {
    bytes.fill(0);
  }
}

function encodeBase64URL(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function decodeBase64URL(value: string) {
  const normalized = String(value || "");
  if (
    !normalized
    || normalized !== normalized.trim()
    || !/^[A-Za-z0-9_-]+$/.test(normalized)
  ) {
    throw new Error("invalid base64url");
  }
  const padded = normalized.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  const decoded = Uint8Array.from(atob(padded), (character) => character.charCodeAt(0));
  if (encodeBase64URL(decoded) !== normalized) {
    throw new Error("non-canonical base64url");
  }
  return decoded;
}
