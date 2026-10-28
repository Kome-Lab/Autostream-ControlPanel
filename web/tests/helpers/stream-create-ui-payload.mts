// Generates the real production form payload; no hand-written JSON substitute.
import { register } from "node:module";
const resolver = [
  "let root;",
  "export function initialize(data) { root=data.root; }",
  "export function resolve(specifier,context,next) {",
  " if(specifier.startsWith('@/')) { const target=new URL('src/'+specifier.slice(2),root); if(!/\\.[cm]?[jt]sx?$/.test(target.pathname))target.pathname+='.ts'; return next(target.href,context); }",
  " return next(specifier,context);",
  "}",
].join("\n");
register(`data:text/javascript,${encodeURIComponent(resolver)}`, { parentURL: import.meta.url, data: {root:new URL("../../",import.meta.url).href} });
const {buildStreamCreatePayload}=await import("../../src/lib/stream-create.ts");
const {buildStreamCreateVisualExtension,defaultStreamVisualDraft}=await import("../../src/features/streams/stream-visual-draft.ts");
let input="";
for await(const chunk of process.stdin)input+=chunk;
const supplied=JSON.parse(input);
const visual={...defaultStreamVisualDraft(),...supplied.visual};
process.stdout.write(JSON.stringify({...buildStreamCreatePayload(supplied.values),...buildStreamCreateVisualExtension(visual)}));
