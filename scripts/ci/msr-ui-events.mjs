import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const [inventoryPath, eventsPath] = process.argv.slice(2);
assert.ok(inventoryPath && eventsPath, 'explicit MSR inventory and TAP event files required');
const required = readFileSync(inventoryPath, 'utf8').split(/\r?\n/).filter((x) => x && !x.startsWith('#'));
assert.ok(required.length > 0, 'required root inventory must not be empty');
assert.equal(required.length, new Set(required).size, 'duplicate required roots');
const text = readFileSync(eventsPath, 'utf8');
assert.match(text, /^TAP version 13$/m, 'test runner did not emit TAP');
assert.doesNotMatch(text, /^\s*not ok /m, 'failed or cancelled test event');
assert.doesNotMatch(text, /^\s*(?:not )?ok \d+ .* # (?:SKIP|TODO)(?:\s|$)/im, 'required run contains skipped or TODO tests');
for (const kind of ['fail', 'cancelled', 'skipped', 'todo']) assert.match(text, new RegExp('^# ' + kind + ' 0$', 'm'), 'nonzero or missing ' + kind + ' summary');
const declared = [...text.matchAll(/^\s*# Subtest: (MSR-UI-.+)$/gm)].map((x) => x[1]);
const roots = [...text.matchAll(/^\s*ok \d+ - (MSR-UI-.+)$/gm)].map((x) => x[1]);
assert.deepEqual([...declared].sort(), [...required].sort(), 'declared roots differ from required inventory');
assert.deepEqual([...roots].sort(), [...required].sort(), 'executed roots differ from required inventory');
console.log(JSON.stringify({ roots: roots.length, tests: Number(text.match(/^# tests (\d+)$/m)?.[1]), skip: 0, fail: 0, cancelled: 0 }));
