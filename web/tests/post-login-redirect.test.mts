import assert from "node:assert/strict";
import test from "node:test";

import { loginPathForLocation, safePostLoginPath } from "../src/lib/auth/post-login-redirect.ts";

test("admin paths keep their query and hash", () => {
  assert.equal(
    safePostLoginPath("/admin/streams/?status=waiting#create-stream"),
    "/admin/streams/?status=waiting#create-stream",
  );
  assert.equal(safePostLoginPath("/admin"), "/admin");
});

test("external, malformed, and non-admin redirects fall back to the dashboard", () => {
  for (const value of [
    "https://evil.example/admin/",
    "//evil.example/admin/",
    "/login",
    "/auth/oauth/callback",
    "/admin\\@evil.example/",
    "/admin/%5cevil.example/",
    "/admin/%0d%0aLocation:%20https://evil.example/",
    "/admin/%c2%85Location:%20https://evil.example/",
    "/admin/../login",
    "/admin/%",
  ]) {
    assert.equal(safePostLoginPath(value), "/admin/", value);
  }
});

test("session timeout login URL carries a validated local return path", () => {
  const loginPath = loginPathForLocation(
    {
      pathname: "/admin/streams/",
      search: "?status=waiting",
      hash: "#create-stream",
    },
    true,
  );
  const parsed = new URL(loginPath, "https://control.example.com");

  assert.equal(parsed.pathname, "/login");
  assert.equal(parsed.searchParams.get("reason"), "session_expired");
  assert.equal(parsed.searchParams.get("redirect_after"), "/admin/streams/?status=waiting#create-stream");
});
