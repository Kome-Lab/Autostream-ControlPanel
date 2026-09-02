import { APIError, getCSRFToken } from "@/lib/api/client";
import type { NodeActionControllerDependencies } from "@/features/nodes/node-action-controller";

type NodeMutationRequest = Parameters<NodeActionControllerDependencies["mutate"]>[0];

export async function requestNodeActionMutation(request: NodeMutationRequest): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(request.path, {
      method: request.method,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "X-CSRF-Token": getCSRFToken(),
        ...(request.body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: request.body === undefined ? undefined : JSON.stringify(request.body),
      signal: request.signal,
    });
  } catch (error) {
    throw error;
  }
  if (response.status === 204) {
    if (!response.ok) throw new APIError("API request failed.", response.status);
    return undefined;
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (!/^application\/json(?:\s*;|$)/i.test(contentType)) {
    throw new APIError("API response was not JSON.", response.status, "non_json_response", contentType);
  }
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new APIError("API response JSON could not be parsed.", response.status, "invalid_json_response", contentType);
  }
  if (!response.ok) {
    const code = safeErrorCode(body);
    throw new APIError("API request failed.", response.status, code, contentType);
  }
  return body;
}

function safeErrorCode(value: unknown) {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
  try {
    const code = Reflect.get(value, "code");
    return typeof code === "string" && code.length <= 128 ? code : undefined;
  } catch {
    return undefined;
  }
}
