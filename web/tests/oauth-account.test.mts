import assert from "node:assert/strict";
import test from "node:test";

import { oauthAccountConfiguredName, oauthAccountDisplayName } from "../src/lib/oauth-account.ts";

test("configured account label is used verbatim", () => {
  const account = {
    id: "3bc83419-6a73-443b-8e1d-e6a4e496a51b",
    provider_type: "google",
    provider_name: "Google Workspace",
    account_label: "第1スタジオ YouTube",
    display_name: "Google接続アカウント",
    email: "studio-1@example.com",
  };

  assert.equal(oauthAccountConfiguredName(account), "第1スタジオ YouTube");
  assert.equal(oauthAccountDisplayName(account), "第1スタジオ YouTube");
});

test("legacy accounts receive distinct non-email fallbacks", () => {
  const first = {
    id: "11111111-6a73-443b-8e1d-e6a4e496a51b",
    provider_type: "google",
    provider_name: "Google Workspace",
    account_label: "studio-1@example.com",
    display_name: "Google接続アカウント",
    email: "studio-1@example.com",
  };
  const second = { ...first, id: "22222222-6a73-443b-8e1d-e6a4e496a51b", account_label: "studio-2@example.com", email: "studio-2@example.com" };

  assert.equal(oauthAccountConfiguredName(first), "");
  assert.equal(oauthAccountDisplayName(first), "Google Workspace (11111111)");
  assert.equal(oauthAccountDisplayName(second), "Google Workspace (22222222)");
  assert.notEqual(oauthAccountDisplayName(first), oauthAccountDisplayName(second));
});
