"use client";

type PublicKeyCredentialUserEntityJSON = Omit<PublicKeyCredentialUserEntity, "id"> & { id: string };
type PublicKeyCredentialDescriptorJSON = Omit<PublicKeyCredentialDescriptor, "id"> & { id: string };
type PublicKeyCredentialCreationOptionsJSON = Omit<PublicKeyCredentialCreationOptions, "challenge" | "user" | "excludeCredentials"> & {
  challenge: string;
  user: PublicKeyCredentialUserEntityJSON;
  excludeCredentials?: PublicKeyCredentialDescriptorJSON[];
};
type PublicKeyCredentialRequestOptionsJSON = Omit<PublicKeyCredentialRequestOptions, "challenge" | "allowCredentials"> & {
  challenge: string;
  allowCredentials?: PublicKeyCredentialDescriptorJSON[];
};

export function passkeysSupported() {
  return typeof window !== "undefined" && "PublicKeyCredential" in window && Boolean(navigator.credentials);
}

export function publicKeyCreationOptionsFromJSON(input: Record<string, unknown>): PublicKeyCredentialCreationOptions {
  const options = input as Partial<PublicKeyCredentialCreationOptionsJSON>;
  const user = options.user || { id: "", name: "", displayName: "" };
  return {
    ...options,
    challenge: base64URLToBuffer(String(options.challenge || "")),
    user: { ...user, id: base64URLToBuffer(String(user.id || "")) },
    excludeCredentials: Array.isArray(options.excludeCredentials)
      ? options.excludeCredentials.map((credential) => ({ ...credential, id: base64URLToBuffer(String(credential.id || "")) }))
      : undefined,
  } as PublicKeyCredentialCreationOptions;
}

export function publicKeyRequestOptionsFromJSON(input: Record<string, unknown>): PublicKeyCredentialRequestOptions {
  const options = input as Partial<PublicKeyCredentialRequestOptionsJSON>;
  return {
    ...options,
    challenge: base64URLToBuffer(String(options.challenge || "")),
    allowCredentials: Array.isArray(options.allowCredentials)
      ? options.allowCredentials.map((credential) => ({ ...credential, id: base64URLToBuffer(String(credential.id || "")) }))
      : undefined,
  } as PublicKeyCredentialRequestOptions;
}

export function passkeyRegistrationCredentialToJSON(credential: PublicKeyCredential) {
  const response = credential.response as AuthenticatorAttestationResponse & { getTransports?: () => string[] };
  return {
    id: credential.id,
    type: credential.type,
    rawId: bufferToBase64URL(credential.rawId),
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      attestationObject: bufferToBase64URL(response.attestationObject),
      transports: response.getTransports ? response.getTransports() : undefined,
    },
  };
}

export function passkeyAssertionCredentialToJSON(credential: PublicKeyCredential) {
  const response = credential.response as AuthenticatorAssertionResponse;
  return {
    id: credential.id,
    type: credential.type,
    rawId: bufferToBase64URL(credential.rawId),
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      authenticatorData: bufferToBase64URL(response.authenticatorData),
      signature: bufferToBase64URL(response.signature),
      userHandle: response.userHandle ? bufferToBase64URL(response.userHandle) : null,
    },
  };
}

function base64URLToBuffer(value: string): ArrayBuffer {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");
  const binary = window.atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes.buffer;
}

function bufferToBase64URL(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return window.btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}
