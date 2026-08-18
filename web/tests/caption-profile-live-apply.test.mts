import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const source = readFileSync(new URL("../src/features/resources/resource-page.tsx", import.meta.url), "utf8");

test("caption profile edit explains saved settings when live apply fails and refreshes the list", () => {
  assert.match(source, /caption_profile_saved_runtime_apply_failed/);
  assert.match(source, /字幕設定は保存されましたが、配信中のWorkerへの即時反映に失敗しました/);
  assert.match(
    source,
    /error\.code === "caption_profile_saved_runtime_apply_failed"[\s\S]*invalidateQueries\(\{ queryKey: \["resource", resource\.path\] \}\)/,
  );
});
