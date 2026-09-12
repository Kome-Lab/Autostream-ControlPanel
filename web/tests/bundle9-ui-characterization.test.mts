import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { declarations, sourceGraph } from "./helpers/bundle9-source-characterization.mts";

const webRoot = fileURLToPath(new URL("..", import.meta.url));
const fixture = JSON.parse(readFileSync(new URL("./fixtures/bundle9-ui-characterization.json", import.meta.url), "utf8")) as {
  beforeCommit: string;
  clusters: { name: string; sources: { path: string; declarations: { name: string; sha256: string; occurrences: number }[] }[] }[];
};

test("Bundle 9 characterization retains the accepted immutable before source", () => {
  assert.equal(fixture.beforeCommit, "792c6c56506c26ffd81b18c1793211e8e78be44d");
  assert.equal(fixture.clusters.length, 5);
  assert.ok(fixture.clusters.every((cluster) => cluster.sources.length > 0));
});

for (const cluster of fixture.clusters) {
  test(`Bundle 9 ${cluster.name}: reachable behavior declarations match before`, () => {
    const graph = new Map(cluster.sources.flatMap((source) => [...sourceGraph(webRoot, source.path)]));
    const actual = [...graph.values()].flat();
    for (const source of cluster.sources) {
      assert.ok(source.declarations.length > 0, `${source.path}: empty baseline`);
      for (const expected of source.declarations) {
        const matches = actual.filter((declaration) => declaration.name === expected.name && declaration.sha256 === expected.sha256);
        assert.ok(expected.occurrences > 0, `${source.path}: ${expected.name} has no before witness`);
        assert.equal(matches.length, expected.occurrences, `${source.path}: ${expected.name} changed, duplicated, or disconnected from its caller`);
      }
    }
  });
}

test("Bundle 9 declaration oracle rejects query, mutation, permission, and JSX changes", () => {
  const source = `export function Example() { useQuery({queryKey: ["resource", path], enabled: canRead, refetchInterval: 15000}); if (canWrite) mutate({revision: 3}); return <button className="same">保存</button>; }`;
  const before = declarations(source, "example.tsx");
  assert.deepEqual(declarations(source.replace("export ", ""), "moved.tsx"), before);
  assert.deepEqual(declarations(source.replaceAll(";", ";\r\n"), "checkout.tsx"), declarations(source.replaceAll(";", ";\n"), "git.tsx"));
  for (const [from, to] of [["15000", "30000"], ["canRead", "true"], ["revision: 3", "revision: 4"], ["canWrite", "true"], ["same", "changed"], ["保存", "更新"]]) {
    assert.notDeepEqual(declarations(source.replace(from!, to!), "example.tsx"), before);
  }
});
