import assert from 'node:assert/strict';
import { writeFileSync } from 'node:fs';
import { RealBrowser } from '/r6/cp/scripts/ci/ui188-r4-chain/real_browser.mts';

const browser = await RealBrowser.launch();
try {
  await browser.navigate('http://127.0.0.1:18840/');
  const positive = await browser.evaluate<string>('document.body.innerText');
  assert.equal(positive, 'UI188_LOOPBACK_CANARY');
  const blocked: boolean[] = [];
  for (const target of ['https://1.1.1.1/', 'https://example.com/', 'http://169.254.169.254/']) {
    blocked.push(await browser.evaluate<boolean>(`fetch(${JSON.stringify(target)},{mode:'no-cors',signal:AbortSignal.timeout(2500)}).then(()=>false,()=>true)`));
  }
  assert.ok(blocked.every(Boolean));
  writeFileSync('/evidence/browser-canary.json', JSON.stringify({positive: true, denied: blocked, normalSandbox: true}) + '\n');
} finally {
  await browser.close();
}
