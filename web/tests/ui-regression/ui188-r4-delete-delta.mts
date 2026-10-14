import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import {readFileSync} from "node:fs";
const hash=(b:Buffer)=>createHash("sha256").update(b).digest("hex");
const bytes=readFileSync(new URL("../fixtures/ui-regression/ui188-r4-delete-deltas.json",import.meta.url));
assert.equal(hash(bytes),"f5764fd5ccd3576843a2f79a6f0bb59f7766d13d416cc4161d8fa756e14a35df","R4 independent finite contract changed");
export const deleteCopyDeltas=JSON.parse(bytes.toString("utf8")).records as readonly {path:string;beforeCommit:string;beforeSha256:string;afterSha256:string}[];
assert.equal(deleteCopyDeltas.length,3);assert.equal(new Set(deleteCopyDeltas.map(r=>r.path)).size,3);
export function historicalR4DeleteCopySource(path:string,current:Buffer,object:(ref:string,path:string)=>Buffer):Buffer{
 const row=deleteCopyDeltas.find(r=>r.path===path);if(!row)return current;
 assert.equal(hash(current),row.afterSha256,"R4 unreviewed current delete copy");
 const before=object(row.beforeCommit,path);assert.equal(hash(before),row.beforeSha256,"R4 fixed delete-copy original mismatch");return before;
}
