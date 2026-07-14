import { readFileSync, readdirSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const srcRoot = join(here, '..', 'src');
const i18nSource = readFileSync(join(srcRoot, 'lib', 'i18n.ts'), 'utf8');
const source = readTSXFiles(srcRoot).join('\n');

const requiredFragments = [
  'export const supportedLocales: Locale[] = ["ja", "en"];',
  'export const localeStorageKey = "autostream.controlPanel.locale";',
  'export const translations = {',
  'document.documentElement.lang = nextLocale',
  '<I18nProvider>',
  '<TooltipProvider',
  'useI18n()',
];

const criticalTranslations = new Map([
  ['dashboard', '\u30c0\u30c3\u30b7\u30e5\u30dc\u30fc\u30c9'],
  ['streams', '\u914d\u4fe1\u67a0'],
  ['auditLogs', '\u76e3\u67fb\u30ed\u30b0'],
  ['status', '\u72b6\u614b'],
  ['actions', '\u64cd\u4f5c'],
  ['nodeRegistration', '\u30ce\u30fc\u30c9\u767b\u9332'],
]);

const missingFragments = requiredFragments.filter((fragment) => !(source + i18nSource).includes(fragment));
const missingTranslations = [...criticalTranslations].filter(([key, value]) => !i18nSource.includes(`${key}: "${value}"`));

const mojibakeFragments = [0x7e67, 0x7e3a, 0x87b3, 0x8703, 0x9036, 0x8b5b, 0x8b2b]
  .map((codePoint) => String.fromCodePoint(codePoint));
const mojibakeHits = mojibakeFragments.filter((fragment) => i18nSource.includes(fragment) || source.includes(fragment));

if (missingFragments.length > 0 || missingTranslations.length > 0 || mojibakeHits.length > 0) {
  if (missingFragments.length > 0) {
    console.error('Missing required i18n guardrail fragments:');
    for (const fragment of missingFragments) console.error(`- ${fragment}`);
  }
  if (missingTranslations.length > 0) {
    console.error('Missing critical Japanese translations:');
    for (const [key] of missingTranslations) console.error(`- ${key}`);
  }
  if (mojibakeHits.length > 0) {
    console.error(`Possible mojibake fragments in dictionary: ${mojibakeHits.join(', ')}`);
  }
  process.exit(1);
}

console.log('i18n guardrails passed for Next.js admin UI.');

function readTSXFiles(dir) {
  const files = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const stats = statSync(full);
    if (stats.isDirectory()) {
      files.push(...readTSXFiles(full));
      continue;
    }
    if (full.endsWith('.tsx') || full.endsWith('.ts')) {
      files.push(readFileSync(full, 'utf8'));
    }
  }
  return files;
}
