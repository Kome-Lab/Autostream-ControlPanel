// Contract fixture invokes the same producer used by the real browser. These
// samples are synthetic unit inputs, never actual UI or Full E2E evidence.
import {readFileSync} from 'node:fs';
import {coverProof,readIdentity} from './cover_proof.mts';
const input=JSON.parse(readFileSync(0,'utf8'));
process.stdout.write(JSON.stringify(coverProof(readIdentity(),input.slot,input.live,input.observed,input.frames))+'\n');
