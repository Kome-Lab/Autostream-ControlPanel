import { mockGet, mockPost, mockPut, mockPathExists } from "@/features/mock-data";

const csrfStorageKey = "autostream.csrf_token";

export class APIError extends Error {
  status: number;
  code?: string;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
  }
}

export function getCSRFToken() {
  if (typeof window === "undefined") return "";
  return window.sessionStorage.getItem(csrfStorageKey) || "";
}

export function setCSRFToken(value?: string) {
  if (typeof window === "undefined" || !value) return;
  window.sessionStorage.setItem(csrfStorageKey, value);
}

export function clearCSRFToken() {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(csrfStorageKey);
}

export async function apiGet<T>(path: string): Promise<T> {
  if (forceMock()) return mockGet(path) as T;
  try {
    const response = await fetch(path, {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    return await readJSONResponse<T>(response, path);
  } catch (error) {
    if (canUseMockFallback(path)) return mockGet(path) as T;
    throw error;
  }
}

export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  if (forceMock()) return mockPost(path, body) as T;
  try {
    const response = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
        "X-CSRF-Token": getCSRFToken(),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return await readJSONResponse<T>(response, path);
  } catch (error) {
    if (canUseMockFallback(path)) return mockPost(path, body) as T;
    throw error;
  }
}

export async function apiPut<T>(path: string, body?: unknown): Promise<T> {
  if (forceMock()) return mockPut(path, body) as T;
  try {
    const response = await fetch(path, {
      method: "PUT",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
        "X-CSRF-Token": getCSRFToken(),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    return await readJSONResponse<T>(response, path);
  } catch (error) {
    if (canUseMockFallback(path)) return mockPut(path, body) as T;
    throw error;
  }
}

async function readJSONResponse<T>(response: Response, path: string): Promise<T> {
  const contentType = response.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    if (canUseMockFallback(path)) return mockGet(path) as T;
    throw new APIError("API response was not JSON.", response.status);
  }
  const data = await response.json();
  if (!response.ok) {
    throw new APIError(data?.code || "API request failed.", response.status, data?.code);
  }
  if (data?.csrf_token) setCSRFToken(data.csrf_token);
  return data as T;
}

function forceMock() {
  return process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO === "true";
}

function canUseMockFallback(path: string) {
  if (process.env.NEXT_PUBLIC_AUTOSTREAM_DEMO === "false") return false;
  if (typeof window === "undefined") return false;
  if (String(path || "").split("?")[0] === "/auth/me") return false;
  const devPorts = new Set(["3000", "3001", "3002", "5173"]);
  return devPorts.has(window.location.port) && mockPathExists(path);
}
