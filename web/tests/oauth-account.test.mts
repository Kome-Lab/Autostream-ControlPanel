import assert from "node:assert/strict";
import test from "node:test";

import {
  oauthAccountConfiguredName,
  oauthAccountDisplayName,
  oauthAccountPurpose,
  oauthAccountPurposeLabel,
  oauthAccountSupportsPurpose,
} from "../src/lib/oauth-account.ts";

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

test("account purpose is derived from granted scopes", () => {
  const drive = { scopes: ["openid", "https://www.googleapis.com/auth/drive.file"] };
  const youtube = { scopes: ["https://www.googleapis.com/auth/youtube.force-ssl"] };
  const both = { scopes: ["https://www.googleapis.com/auth/drive", "https://www.googleapis.com/auth/youtube"] };

  assert.equal(oauthAccountPurpose(drive), "drive");
  assert.equal(oauthAccountPurpose(youtube), "youtube");
  assert.equal(oauthAccountPurpose(both), "drive_youtube");
  assert.equal(oauthAccountPurposeLabel(both), "YouTube Live・Drive保存");
  assert.equal(oauthAccountSupportsPurpose(drive, "drive"), true);
  assert.equal(oauthAccountSupportsPurpose(drive, "youtube"), false);
  assert.equal(oauthAccountSupportsPurpose(both, "drive"), true);
  assert.equal(oauthAccountSupportsPurpose(both, "youtube"), true);
});

test("account purpose falls back to the API field when scopes are absent", () => {
  const legacy = { account_purpose: "youtube" };
  assert.equal(oauthAccountPurpose(legacy), "youtube");
  assert.equal(oauthAccountSupportsPurpose(legacy, "youtube"), true);
  assert.equal(oauthAccountSupportsPurpose(legacy, "drive"), false);
});
