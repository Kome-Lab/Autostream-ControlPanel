import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// Fixed release assembly over the prior main: CP077, the resolved Contracts
// module, and the approved split of CP/legacy-Updater CI dependency checks.
// This is a byte-bound supplement, not a new UI acceptance or a hash refresh.
export const releaseAssemblyBase = "fade8df68aacc2ce484a2ace470f12789eb83f82";
export const releaseAssemblySource = "195bb0a5c90dd35ed911e5ba5e506efee13d3843";
const records = [
  {
    "path": ".github/workflows/ci.yml",
    "beforeSha256": "38f3a29f45da8993d4d987b07a991c6096acf70cd4e7c511a5eda5ea05a32bb6",
    "afterSha256": "36e683476dffffdcd2565a9968ee0f958ec82cbda3c621305058c9931d1fc0fc"
  },
  {
    "path": "go.mod",
    "beforeSha256": "66aba3ffaeb2a4ccb042784bf1f3f08bea320d4e098cb87569b65ef2efd8d85c",
    "afterSha256": "4d5423a6e1351f8dac9b37cad5c24257d0c3bbb6e5d471f2b5987621a2394ffc"
  },
  {
    "path": "go.sum",
    "beforeSha256": "0b87713e3959cf3b837e7cc1de19fe671d3ae3ff8120bcd162e1b139efcf8683",
    "afterSha256": "de6e07f1e240be28eae153d8980baa61b49c4b9c4c95b05b1f85eb7cb2083f55"
  },
  {
    "path": "internal/httpapi/server_runtime_secrets.go",
    "beforeSha256": "08384b23ced9331ab07f6cacfd4dd2bc4a2ecec1730fbcd5f7997d5d09510354",
    "afterSha256": "b59938df6df200342a88f36ae6954a4052904914da2f5dba1ff659488a2d9bf8"
  },
  {
    "path": "internal/httpapi/server_stream_lifecycle_test.go",
    "beforeSha256": "5d0df43ff6e6900583f5ca6370fba8a1df8bb6acf0e1a1e46565139a0be12f6f",
    "afterSha256": "96e1956b79214b384adbada9c9f855de56ece519c4fe58095c882b6e53862ee3"
  },
  {
    "path": "internal/httpapi/server_stream_start_completion.go",
    "beforeSha256": "74971278896e809d13ccaa796cde07b27a5c1d551ded39db561c95d8d2a3533a",
    "afterSha256": "9eb0027f3c2e450e17ac317217d79057507fc373b0d30fe424b9035519d92103"
  },
  {
    "path": "internal/httpapi/server_stream_start_orchestration.go",
    "beforeSha256": "0352e1aec506d5131568771b1238ec976929cbf4424cb3e35b5b6b24ec229ce1",
    "afterSha256": "b194022fdfa43c3129243b932d912f5b1b1bf3507c1b5f848361f1531b636ea1"
  },
  {
    "path": "internal/servicecall/client.go",
    "beforeSha256": "ecf5ee865ea4e59c576ea4279c48f22ac3626c25e76d618e513904653e86dc70",
    "afterSha256": "6479a60c491e491d95efad2bb82b9a71f3c40a40d1b5f5ac2876afb1fc6b8283"
  },
  {
    "path": "internal/servicecall/client_start_orchestration.go",
    "beforeSha256": "ad6b305f022a98f60b6233e6cc627f792574b2d0f81732a4102513c88dd13b21",
    "afterSha256": "a994999c8c4ae5db13b99f2bfb575f722beaa9680eedf1e7bcecdd7709cdef6d"
  },
  {
    "path": "internal/servicecall/client_worker_video_test.go",
    "beforeSha256": "1032eb5d4cede6555ff4e461841e7433c610da3cad12db8c922f1d76a505eb5a",
    "afterSha256": "b534825b402ea42fd476c9ea4ea079f3403611733e8cceb72f111e2651c3868f"
  },
  {
    "path": "internal/store/discord_youtube_live_notification_outbox.go",
    "beforeSha256": "ab4380c10810f9b525548dccee455eb497b014eeb804ac2368b03ea00856ae8a",
    "afterSha256": "35daf9a83e046e695bc81794a7a016dd4b708e4ab547b0b20c946021da17293f"
  }
] as const;
export const releaseAssemblyPaths: readonly string[] = records.map(row => row.path);
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");

export function inverseReleaseAssembly(path: string, before: Buffer, current: Buffer): Buffer {
  const row = records.find(item => item.path === path);
  assert.ok(row, "unknown release assembly path");
  assert.equal(hash(before), row.beforeSha256, "release assembly fixed original hash mismatch");
  assert.equal(hash(current), row.afterSha256, "normalization permits only exact specified name edits or fixed release assembly bytes");
  return before;
}
