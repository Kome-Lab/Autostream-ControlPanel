import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// Exact, reviewed CI closure over the delivered 085 source. Historical UI
// authority and the eleven 085 assembly bindings remain byte-for-byte fixed.
export const ciClosureBase = "623dd7f55b4f0023af9586c0f354435ecce3ae96";
const records = [
  {
    "path": "internal/httpapi/server_start_preparation_claim_test.go",
    "beforeSha256": "94e03c21a5d66ff153f71dacd1f22e527c5e4c83d2e9f3f575207ff81df1ab7f",
    "afterSha256": "2e5c0a486f19731f4fcf5e0ffcee8eb0dd589798db5628930d989353b97e7407"
  },
  {
    "path": "internal/store/memory_stream_start_claim.go",
    "beforeSha256": "a9b39049d470554ea877e20e192c28fbd4c3a57d053627434c92fb4c69b9e28c",
    "afterSha256": "9b227a0b7c72f5919bb9ce226f14f774f7c12c12756bc9da994b3cf6f3dd9265"
  },
  {
    "path": "internal/store/stream_start_claim_mariadb.go",
    "beforeSha256": "25d228cc388e0576723414b811404716d128812695ea74ed63ad135e7f5a18a5",
    "afterSha256": "f155ad76baef3d96605a5fa7f9e424ff31ce3e46b7390b21286cf9a2449817bf"
  },
  {
    "path": "web/tests/helpers/browser-harness.mts",
    "beforeSha256": "a0c4616658594c7feb2b6f5af873374ff1d921b36cf4448b42e608e33899150a",
    "afterSha256": "3217b57b2cf22bb9bb048779bc203c8d5d1e91f872b3d865204641b42382386a"
  },
  {
    "path": "web/tests/ui-browser-account-scenarios.mts",
    "beforeSha256": "1ce83295dc3b4d18e3d57ceae4c3ea5541bbf2a896a985ca9fe77ab90e02fb8b",
    "afterSha256": "77bafc13d975a8547a8283bac568ca008d92aa607c5e9d9936f2b3f0517e34cc"
  }
] as const;
export const ciClosurePaths: readonly string[] = records.map(row => row.path);
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
export function inverseCIClosure(path: string, before: Buffer, current: Buffer): Buffer {
  const row = records.find(item => item.path === path);
  assert.ok(row, "unknown CI closure path");
  assert.equal(hash(before), row.beforeSha256, "CI closure fixed original hash mismatch");
  assert.equal(hash(current), row.afterSha256, "normalization permits only exact specified name edits or fixed CI closure bytes");
  return before;
}
