import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";

type Locator = { path: string; sourceSha256: string; vocabularyIds: string[] };
type Vocabulary = { id: string; authorityId: string; excerpt: string };

/** Read the actual raw object closure; the caller records whether it is a code commit or a local candidate tree. */
export function verifyStatusAuthoritySourceLocators(
  root: string,
  authorityId: string,
  revision: string,
  locators: Locator[],
  vocabularies: Vocabulary[],
) {
  assert.match(revision, /^[a-f0-9]{40}$/, "source authority requires a fixed full object ID");
  assert.ok(Array.isArray(locators) && locators.length > 0, `${authorityId}: missing current source locators`);
  const owned = vocabularies.filter((entry) => entry.authorityId === authorityId);
  const expected = new Set(owned.map((entry) => entry.id));
  const witnessed = new Set<string>();
  const paths = new Set<string>();
  for (const locator of locators) {
    assert.match(locator.path, /^[A-Za-z0-9_./-]+$/);
    assert.ok(!locator.path.startsWith("/") && locator.path.split("/").every((part) => part && part !== "." && part !== ".."));
    assert.equal(paths.has(locator.path), false, `${authorityId}: duplicate source path`);
    paths.add(locator.path);
    assert.match(locator.sourceSha256, /^[a-f0-9]{64}$/);
    assert.ok(Array.isArray(locator.vocabularyIds) && locator.vocabularyIds.length > 0, `${authorityId}: source has no vocabulary witness`);
    const entry = execFileSync("git", ["-C", root, "ls-tree", revision, "--", locator.path], { encoding: "utf8" });
    assert.match(entry, /^100644 blob [a-f0-9]{40}\t/, `${authorityId}: current authority is not a regular Git blob`);
    const bytes = execFileSync("git", ["-C", root, "cat-file", "blob", `${revision}:${locator.path}`]);
    assert.equal(createHash("sha256").update(bytes).digest("hex"), locator.sourceSha256, `${authorityId}:${locator.path} raw source digest`);
    const text = bytes.toString("utf8").replace(/\r\n/g, "\n");
    for (const id of locator.vocabularyIds) {
      assert.equal(expected.has(id), true, `${authorityId}: unowned vocabulary locator`);
      assert.equal(witnessed.has(id), false, `${authorityId}: duplicate vocabulary witness`);
      const vocabulary = owned.find((entry) => entry.id === id)!;
      assert.equal(text.includes(vocabulary.excerpt.replace(/\r\n/g, "\n")), true, `${id}: original excerpt is absent from current source`);
      witnessed.add(id);
    }
  }
  assert.deepEqual([...witnessed].sort(), [...expected].sort(), `${authorityId}: current source dropped a vocabulary`);
  return Object.freeze({ sourceFiles: paths.size, vocabularies: witnessed.size });
}
