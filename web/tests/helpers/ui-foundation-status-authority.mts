import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

export function readStatusAuthority(webRoot) {
  return JSON.parse(readFileSync(
    join(webRoot, "tests", "fixtures", "ui-foundation-status-authority.json"),
    "utf8",
  ));
}

export function deriveStatusAuthorityDomains(authority) {
  assert.equal(authority.schemaVersion, 1, "status authority schema changed");
  assert.equal(Array.isArray(authority.authorities), true, "authority source inventory is missing");
  assert.equal(Array.isArray(authority.vocabularies), true, "authority vocabulary inventory is missing");

  const authorityIds = new Set();
  for (const source of authority.authorities) {
    assert.equal(typeof source.id, "string");
    assert.equal(authorityIds.has(source.id), false, `duplicate authority ${source.id}`);
    assert.match(source.sourceSha256, /^[a-f0-9]{64}$/);
    assert.match(source.head, /^[a-f0-9]{40}$/);
    authorityIds.add(source.id);
  }

  const domains = new Map();
  const vocabularyIds = new Set();
  for (const vocabulary of authority.vocabularies) {
    assert.equal(vocabularyIds.has(vocabulary.id), false, `duplicate vocabulary ${vocabulary.id}`);
    assert.equal(authorityIds.has(vocabulary.authorityId), true, `${vocabulary.id} has no source authority`);
    assert.equal(sha256(vocabulary.excerpt), vocabulary.excerptSha256, `${vocabulary.id} excerpt digest`);
    vocabularyIds.add(vocabulary.id);

    const entries = extractVocabulary(vocabulary);
    assert.notEqual(entries.length, 0, `${vocabulary.id} extracted no values`);
    assert.equal(new Set(entries.map((entry) => entry.symbol)).size, entries.length, `${vocabulary.id} has duplicate symbols`);
    assert.equal(new Set(entries.map((entry) => entry.value)).size, entries.length, `${vocabulary.id} has duplicate values`);
    const bySymbol = new Map(entries.map((entry) => [entry.symbol, entry.value]));
    const assignedSymbols = new Set();

    for (const projection of vocabulary.domains) {
      assert.equal(domains.has(projection.name), false, `duplicate domain authority ${projection.name}`);
      const symbols = projection.include === "all"
        ? entries.map((entry) => entry.symbol)
        : projection.symbols;
      assert.equal(Array.isArray(symbols), true, `${vocabulary.id}:${projection.name} has no symbols`);
      const values = [];
      for (const symbol of symbols) {
        assert.equal(bySymbol.has(symbol), true, `${vocabulary.id}:${projection.name} invents ${symbol}`);
        assert.equal(assignedSymbols.has(symbol), false, `${vocabulary.id} assigns ${symbol} twice`);
        assignedSymbols.add(symbol);
        values.push(bySymbol.get(symbol));
      }
      assert.equal(new Set(values).size, values.length, `${projection.name} contains duplicate values`);
      domains.set(projection.name, Object.freeze(values));
    }

    for (const symbol of vocabulary.sentinelSymbols) {
      assert.equal(bySymbol.has(symbol), true, `${vocabulary.id} invents sentinel ${symbol}`);
      assert.equal(assignedSymbols.has(symbol), false, `${vocabulary.id} reuses sentinel ${symbol}`);
      assignedSymbols.add(symbol);
    }
    assert.deepEqual(
      [...assignedSymbols].sort(),
      entries.map((entry) => entry.symbol).sort(),
      `${vocabulary.id} must partition every source symbol into a domain or explicit unknown sentinel`,
    );
  }
  return domains;
}

export function compareStatusMappingInventory(authority, mappingFixture) {
  const expected = deriveStatusAuthorityDomains(authority);
  const errors = [];
  const fixtureDomains = Array.isArray(mappingFixture.domains) ? mappingFixture.domains : [];
  const mappings = Array.isArray(mappingFixture.mappings) ? mappingFixture.mappings : [];
  if (!sameStrings(fixtureDomains, [...expected.keys()])) {
    errors.push("domain inventory differs from source authority");
  }

  const actual = new Map();
  const seen = new Set();
  for (const mapping of mappings) {
    if (!mapping || typeof mapping !== "object"
      || typeof mapping.domain !== "string"
      || typeof mapping.wireValue !== "string") {
      errors.push("malformed mapping row");
      continue;
    }
    const key = `${mapping.domain}\u0000${mapping.wireValue}`;
    if (seen.has(key)) errors.push(`duplicate mapping ${mapping.domain}:${mapping.wireValue}`);
    seen.add(key);
    const values = actual.get(mapping.domain) || [];
    values.push(mapping.wireValue);
    actual.set(mapping.domain, values);
  }

  for (const [domain, values] of expected) {
    const mapped = actual.get(domain) || [];
    if (!sameStrings(mapped, values)) errors.push(`${domain} values differ from source authority`);
  }
  for (const domain of actual.keys()) {
    if (!expected.has(domain)) errors.push(`unowned mapping domain ${domain}`);
  }
  return Object.freeze(errors);
}

export function verifyStatusAuthoritySources(webRoot, authority) {
  const controlPanelRoot = resolve(webRoot, "..");
  const repositoryRoots = Object.freeze({
    "Autostream-ControlPanel": controlPanelRoot,
    "Autostream-Contracts": resolve(controlPanelRoot, "..", "Autostream-Contracts"),
    "Autostream-Observability": resolve(controlPanelRoot, "..", "Autostream-Observability"),
  });
  let verifiedSources = 0;
  const unavailableSources = [];
  for (const source of authority.authorities) {
    const root = repositoryRoots[source.repository];
    assert.equal(typeof root, "string", `unknown authority repository ${source.repository}`);
    const path = join(root, ...source.path.split("/"));
    if (!existsSync(path)) {
      unavailableSources.push(`${source.repository}:${source.path}`);
      continue;
    }
    const bytes = readFileSync(path);
    assert.equal(sha256(bytes), source.sourceSha256, `${source.repository}:${source.path} source digest`);
    const normalizedSource = normalizeNewlines(bytes.toString("utf8"));
    for (const vocabulary of authority.vocabularies.filter((entry) => entry.authorityId === source.id)) {
      assert.equal(
        normalizedSource.includes(normalizeNewlines(vocabulary.excerpt)),
        true,
        `${vocabulary.id} snapshot is not byte-derived from the fixed source`,
      );
    }
    verifiedSources += 1;
  }
  assert.equal(verifiedSources >= 5, true, "repository-contained Control Panel authority was not verified");
  return Object.freeze({
    verifiedSources,
    unavailableSources: Object.freeze(unavailableSources),
    excerptMismatches: 0,
  });
}

function extractVocabulary(vocabulary) {
  switch (vocabulary.parser) {
    case "go-typed-constants":
      return regexEntries(vocabulary.excerpt, /^\s*([A-Za-z]\w*)\s+[A-Za-z]\w*\s*=\s*"([^"]+)"\s*$/gm);
    case "go-untyped-constants":
      return regexEntries(vocabulary.excerpt, /^\s*([A-Za-z]\w*)\s*=\s*"([^"]+)"\s*$/gm);
    case "sql-status-enum": {
      const match = vocabulary.excerpt.match(/\bstatus\s+ENUM\(([^)]+)\)/);
      assert.ok(match, `${vocabulary.id} status ENUM missing`);
      return quotedValues(match[1], vocabulary.id);
    }
    case "ts-string-union":
      return valueEntries([...vocabulary.excerpt.matchAll(/^\s*\|\s*"([^"]+)";?\s*$/gm)].map((match) => match[1]), vocabulary.id);
    case "go-return-literals":
      return valueEntries([...vocabulary.excerpt.matchAll(/\breturn\s+"([^"]+)"/g)].map((match) => match[1]), vocabulary.id);
    case "sql-status-in": {
      const values = [...vocabulary.excerpt.matchAll(/\bstatus\)\)\s+IN\s*\(([^)]+)\)/g)]
        .flatMap((match) => [...match[1].matchAll(/'([^']+)'/g)].map((value) => value[1]));
      return valueEntries(values, vocabulary.id);
    }
    case "go-status-assignments":
      return valueEntries([...vocabulary.excerpt.matchAll(/\bstatus\s*(?::=|=)\s*"([^"]+)"/g)].map((match) => match[1]), vocabulary.id);
    case "go-filter-results":
      return valueEntries([...vocabulary.excerpt.matchAll(/filter\.Result\s*!=\s*"([^"]+)"/g)].map((match) => match[1]), vocabulary.id);
    default:
      assert.fail(`unsupported authority parser ${vocabulary.parser}`);
  }
}

function regexEntries(source, pattern) {
  return [...source.matchAll(pattern)].map((match) => Object.freeze({ symbol: match[1], value: match[2] }));
}

function quotedValues(source, prefix) {
  return valueEntries([...source.matchAll(/'([^']+)'/g)].map((match) => match[1]), prefix);
}

function valueEntries(values, prefix) {
  return [...new Set(values)].map((value) => Object.freeze({ symbol: `${prefix}:${value}`, value }));
}

function sameStrings(left, right) {
  return left.length === right.length
    && new Set(left).size === left.length
    && new Set(right).size === right.length
    && [...left].sort().every((value, index) => value === [...right].sort()[index]);
}

function normalizeNewlines(value) {
  return value.replace(/\r\n/g, "\n");
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}
