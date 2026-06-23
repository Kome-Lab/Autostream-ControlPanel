import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, '..', 'src', 'main.jsx'), 'utf8');

const requiredFragments = [
  "const supportedLocales = ['ja', 'en'];",
  "const localeStorageKey = 'autostream.controlPanel.locale';",
  'function LanguageSwitcher()',
  'document.documentElement.lang = locale',
  '{t(column.label)}',
  'localizeRendered(children ||',
  '<I18nProvider>',
];

const criticalTranslations = new Map([
  ['Dashboard', '\u30c0\u30c3\u30b7\u30e5\u30dc\u30fc\u30c9'],
  ['Streams', '\u914d\u4fe1'],
  ['Audit Logs', '\u76e3\u67fb\u30ed\u30b0'],
  ['Service Health', '\u30b5\u30fc\u30d3\u30b9\u30d8\u30eb\u30b9'],
  ['Status', '\u30b9\u30c6\u30fc\u30bf\u30b9'],
  ['Actions', '\u64cd\u4f5c'],
  ['configured', '\u8a2d\u5b9a\u6e08\u307f'],
  ['missing', '\u672a\u8a2d\u5b9a'],
]);

const dictMatch = source.match(/const textByLocale = \{[\s\S]*?\n\};\n\nconst I18nContext/);
if (!dictMatch) {
  console.error('Unable to locate textByLocale dictionary.');
  process.exit(1);
}

const dictionarySource = dictMatch[0];
const dictionaryKeys = new Set();
for (const match of dictionarySource.matchAll(/^\s*(?:'([^']+)'|"([^"]+)"|([A-Za-z][A-Za-z0-9_]*)):/gm)) {
  dictionaryKeys.add(match[1] || match[2] || match[3]);
}

const labelKeys = [...source.matchAll(/label:\s*'([^']+)'/g)].map((match) => match[1]);
const tableTitleKeys = [...source.matchAll(/DataTable\s+title="([^"]+)"/g)].map((match) => match[1]);
const staticTableKeys = [...new Set([...labelKeys, ...tableTitleKeys])].sort((a, b) => a.localeCompare(b));
const missingDictionaryKeys = staticTableKeys.filter((key) => !dictionaryKeys.has(key));

const missingFragments = requiredFragments.filter((fragment) => !source.includes(fragment));
const missingTranslations = [...criticalTranslations].filter(([key, value]) => !dictionarySource.includes(`${key}: '${value}'`) && !dictionarySource.includes(`'${key}': '${value}'`));

const mojibakeFragments = [0x7e67, 0x7e3a, 0x87b3, 0x8703, 0x9036, 0x8b5b, 0x8b2b]
  .map((codePoint) => String.fromCodePoint(codePoint));
const mojibakeHits = mojibakeFragments.filter((fragment) => dictionarySource.includes(fragment));

if (missingFragments.length > 0 || missingDictionaryKeys.length > 0 || missingTranslations.length > 0 || mojibakeHits.length > 0) {
  if (missingFragments.length > 0) {
    console.error('Missing required i18n guardrail fragments:');
    for (const fragment of missingFragments) console.error(`- ${fragment}`);
  }
  if (missingDictionaryKeys.length > 0) {
    console.error('Missing dictionary keys for static table labels/titles:');
    for (const key of missingDictionaryKeys) console.error(`- ${key}`);
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

console.log(`i18n guardrails passed for ${staticTableKeys.length} table labels/titles.`);
