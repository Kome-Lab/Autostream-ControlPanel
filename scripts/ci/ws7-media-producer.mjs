import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { readFile, stat, writeFile } from 'node:fs/promises';
import { isAbsolute, join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const [binary, source, output] = process.argv.slice(2);
assert.ok(binary && source && output && [binary, source, output].every(isAbsolute), 'explicit isolated binary/source/output paths required');
assert.ok((await stat(binary)).isFile() && (await stat(source)).isDirectory() && (await stat(output)).isDirectory());
const child = spawn(binary, ['-test.run', '^TestIUWS3CrossRepositoryEncoderProcess$', '-test.v'], {
  cwd: source,
  env: { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'local', GOTELEMETRY: 'off', IU_WS3_ER_SOURCE: source, IU_WS3_FIXTURE_DIR: output },
  stdio: ['ignore', 'pipe', 'pipe'],
});
// Consume output without disclosing fixture URLs, raw media or arbitrary errors.
child.stdout.on('data', () => {});
child.stderr.on('data', () => {});
let childError;
child.on('error', () => { childError = new Error('isolated_media_process_start_failed'); });
const finished = once(child, 'exit');
let base;
let finishedRequested = false;
const request = async (path, method = 'GET') => {
  let response;
  try { response = await fetch(new URL(path, base), { method, signal: AbortSignal.timeout(10000) }); }
  catch { throw new Error('isolated_media_http_unavailable'); }
  assert.ok(response.ok, 'isolated_media_http_refused');
  return response;
};
try {
  for (let tries = 0; tries < 400; tries++) {
    if (childError) throw childError;
    assert.equal(child.exitCode, null, 'isolated_media_process_exited_before_ready');
    const file = await readFile(join(output, 'server.url'), 'utf8').catch(() => '');
    if (file) { base = new URL(file.trim()); break; }
    await delay(25);
  }
  assert.ok(base && base.protocol === 'http:' && ['127.0.0.1', '[::1]', '::1'].includes(base.hostname) && base.port && !base.username && !base.password && base.pathname === '/' && !base.search && !base.hash, 'explicit_loopback_fixture_only');
  const body = await (await request('/fixture/closure/feed', 'POST')).text();
  assert.ok(Buffer.byteLength(body) <= 2 * 1024 * 1024, 'bounded_metadata_response_required');
  const record = JSON.parse(body);
  assert.equal(record.Header.ContractRef.ContractID, 'CC08');
  assert.equal(record.Header.ContractRef.ModuleCommitRef, 'd32ea3514b1c1010391caae0348b0ff9e751f447');
  assert.equal(record.Header.ProfileRef.ProfileID, 'G0-P-MEDIA-INGRESS');
  const observations = record.Inspection.Observations;
  assert.equal(observations.Gate, 'HELD');
  const rows = observations.Observations;
  assert.equal(rows.length, 4);
  for (const sub of ['RAW_WRITE', 'DURABILITY', 'QUEUE_ADMISSION']) {
    const row = rows.find((x) => x.Address.Kind === 'LOCAL_PERSISTENCE' && x.Address.SubBoundary === sub);
    assert.equal(row.Knowledge, 'KNOWN');
    assert.equal(row.Outcome.Value, 'SUCCESS_AT_BOUNDARY');
    assert.equal(row.Address.Resource.Kind, sub === 'QUEUE_ADMISSION' ? 'PARTITION' : 'RUN');
  }
  const terminal = rows.find((x) => x.Address.SubBoundary === 'QUEUE_TERMINAL');
  assert.equal(terminal.Knowledge, 'KNOWN');
  assert.equal(terminal.Outcome.Value, 'PENDING');
  assert.ok(observations.PhysicalObligations.some((x) => x.Kind === 'QUEUE' && x.Knowledge === 'UNKNOWN' && x.Terminal.State === 'UNAVAILABLE'));
  assert.ok(observations.RemainingObligations.length > 0);
  assert.ok(!rows.some((x) => x.Address.Kind === 'PROVIDER'));
  const state = await (await request('/fixture/state')).json();
  assert.equal(state.Posts, 1);
  assert.equal(state.Inspections, 0);
  assert.equal(state.RawLines, 1);
  assert.equal(state.QueuePending, 1);
  assert.equal(state.ProductionQualified, false);
  await writeFile(resolve(output, 'ws7-media-ingest.json'), JSON.stringify(record) + '\n', { flag: 'wx', mode: 0o600 });
  await request('/fixture/finish', 'POST');
  finishedRequested = true;
  const [code] = await finished;
  assert.equal(code, 0, 'isolated_media_fixture_failed');
  console.log(JSON.stringify({ media_http_calls: 1, raw_lines: 1, queue_pending: 1, gate: 'HELD', remote_proof: false, production_qualified: false }));
} finally {
  if (!finishedRequested && base && child.exitCode === null) await request('/fixture/finish', 'POST').catch(() => {});
  if (child.exitCode === null) child.kill('SIGTERM');
}
