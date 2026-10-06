import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// CP174 adds only the nil-list response fix, two release UI scripts, and the
// required release UI job. Both byte sides and every edit remain finite; this
// supplement does not replace any historical acceptance or assembly hashes.
export const cp174Base = "0315845e3af01eff6b97c6164db3ddc3109b55af";
const records = [
  {
    "path": "internal/httpapi/server_streams.go",
    "beforeSha256": "5eafbebf64e09855a551377f93f082a81947c065b2ad32542ec9dee004f61c86",
    "afterSha256": "3b1760c9328c333f6af5904d89bfa297f2967b3cb671720bd72dfddf703437d7",
    "edits": [
      {
        "position": 17808,
        "before": "",
        "after": "\t// List responses must stay JSON arrays even when a store returns a nil\n\t// slice for a successful query with no rows.\n\tif streams == nil {\n\t\treturn []store.Stream{}, nil\n\t}\n"
      }
    ]
  },
  {
    "path": "web/package.json",
    "beforeSha256": "bebc441ea0eeff716fff5884d3ff653bbca4104cb4596c78f22cc738f1e737e7",
    "afterSha256": "156b43c64b43a0072bc134d52651b75f08d8d08ebcb23453ac6eb7c03668b64a",
    "edits": [
      {
        "position": 3017,
        "before": "",
        "after": "    \"test:release-ui\": \"node --no-warnings --test --test-concurrency=1 tests/preset-resource-actions.test.mts tests/streams-visual-integration.test.mts tests/ui-regression/preset-components.test.mts tests/ui-regression/polling-state.test.mts tests/ui-regression/node-layout-tabs.test.mts tests/ui-regression/refresh-layout.test.mts\",\n    \"test:release-ui-browser\": \"node --no-warnings --test --test-concurrency=1 tests/ui-refresh-layout-browser.test.mts tests/ui-release-layout-browser.test.mts\",\n"
      }
    ]
  },
  {
    "path": ".github/workflows/ci.yml",
    "beforeSha256": "36e683476dffffdcd2565a9968ee0f958ec82cbda3c621305058c9931d1fc0fc",
    "afterSha256": "85b1b68ff9b9d42292b63bfc73d439ed520263b2d315815407219fd1282928cf",
    "edits": [
      {
        "position": 37265,
        "before": "",
        "after": "  release-ui:\n    name: Release UI polling, layout and presets\n    runs-on: ubuntu-24.04\n    timeout-minutes: 15\n    defaults:\n      run:\n        working-directory: web\n    steps:\n      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n      - uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0\n        with:\n          node-version: 26\n          cache: npm\n          cache-dependency-path: web/package-lock.json\n      - run: npm ci\n      - run: npm run typecheck\n      - run: npm run test:release-ui\n      - name: Prepare Japanese browser fonts\n        run: |\n          sudo apt-get update\n          sudo apt-get install --no-install-recommends -y fontconfig fonts-noto-cjk=1:20230817+repack1-3\n          fc-list -q ':lang=ja'\n      - name: Verify polling state and rendered layouts\n        timeout-minutes: 6\n        env:\n          AUTOSTREAM_BROWSER_PATH: /usr/bin/google-chrome\n          AUTOSTREAM_UI174_EVIDENCE_DIR: ${{ runner.temp }}/release-ui-evidence\n        run: npm run test:release-ui-browser\n      - name: Upload rendered UI evidence\n        if: always()\n        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1\n        with:\n          name: release-ui-evidence-${{ github.sha }}\n          path: ${{ runner.temp }}/release-ui-evidence\n          if-no-files-found: warn\n          retention-days: 7\n\n"
      },
      {
        "position": 37377,
        "before": "    needs: [service-installer, go, release-rehearsal, observability_proxy_race_pre_fix, observability_proxy_race_current, web, ui-regression]\n",
        "after": "    needs: [service-installer, go, release-rehearsal, observability_proxy_race_pre_fix, observability_proxy_race_current, web, release-ui, ui-regression]\n"
      },
      {
        "position": 38056,
        "before": "",
        "after": "          RELEASE_UI_RESULT: ${{ needs.release-ui.result }}\n"
      },
      {
        "position": 38508,
        "before": "",
        "after": "          [[ \"${RELEASE_UI_RESULT}\" == success ]]\n"
      }
    ]
  }
] as const;
export const cp174Paths: readonly string[] = records.map(row => row.path);
export const cp174ProtectedPaths: readonly string[] = ["internal/httpapi/server_streams.go"];
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const recordFor = (path: string) => {
  const row = records.find(item => item.path === path);
  assert.ok(row, "unknown CP174 source path");
  return row;
};

export function assertCP174Original(path: string, before: Buffer) {
  assert.equal(hash(before), recordFor(path).beforeSha256, "CP174 fixed original hash mismatch");
}

export function assertCP174SourceDelta(path: string, before: Buffer, current: Buffer): Buffer {
  const row = recordFor(path);
  assertCP174Original(path, before);
  assert.equal(hash(current), row.afterSha256, "normalization permits only exact specified name edits or fixed CP174 bytes");
  let forward = before.toString("utf8"), offset = 0, end = 0;
  for (const edit of row.edits) {
    assert.ok(Number.isSafeInteger(edit.position) && edit.position >= end, "CP174 finite edit position/order");
    const at = edit.position + offset;
    assert.equal(forward.slice(at, at + edit.before.length), edit.before, "CP174 exact original edit");
    forward = forward.slice(0, at) + edit.after + forward.slice(at + edit.before.length);
    end = edit.position + edit.before.length;
    offset += edit.after.length - edit.before.length;
  }
  assert.deepEqual(current, Buffer.from(forward), "normalization permits only exact specified name edits or finite CP174 edits");
  return before;
}

export function inverseCP174Source(path: string, current: Buffer): Buffer {
  const row = recordFor(path);
  assert.equal(hash(current), row.afterSha256, "normalization permits only exact specified name edits or fixed CP174 bytes");
  let inverse = current.toString("utf8");
  let offset = row.edits.reduce((sum, edit) => sum + edit.after.length - edit.before.length, 0);
  for (const edit of [...row.edits].reverse()) {
    offset -= edit.after.length - edit.before.length;
    const at = edit.position + offset;
    assert.equal(inverse.slice(at, at + edit.after.length), edit.after, "CP174 exact inverse edit");
    inverse = inverse.slice(0, at) + edit.before + inverse.slice(at + edit.after.length);
  }
  return assertCP174SourceDelta(path, Buffer.from(inverse), current);
}
