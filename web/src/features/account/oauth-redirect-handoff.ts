import { APIError } from "@/lib/api/client";

export function validatedOAuthRedirect(value: unknown) {
  if (typeof value !== "string" || value.length === 0 || value.length > 16_384) {
    throw invalidRedirect();
  }
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw invalidRedirect();
  }
  const localHTTP = parsed.protocol === "http:" && ["localhost", "127.0.0.1", "::1"].includes(parsed.hostname);
  if (parsed.protocol !== "https:" && !localHTTP) throw invalidRedirect();
  return parsed.href;
}

function invalidRedirect() {
  return new APIError("Invalid OAuth redirect response.", 502, "invalid_oauth_redirect_response");
}
