"""Finite test-overlay authority; the historical manifest is never redefined."""
import copy
import hashlib
import json
import unittest
from source import HERE,CP_TEST_PATHS,cp_test_rows,tree_hash,unique_json,verify
from gate import Blocked


class SourceOverlayTests(unittest.TestCase):
    def setUp(self):
        self.manifest=json.loads((HERE/'product-files.json').read_text())
        self.lock=json.loads((HERE/'source-lock.json').read_text())
        self.overlay=json.loads((HERE/'cp-test-overlay.json').read_text())

    def check(self,value,manifest=None):
        raw=json.dumps(value,sort_keys=True).encode()
        lock=copy.deepcopy(self.lock)
        lock['cp_test_overlay_sha256']=hashlib.sha256(raw).hexdigest()
        return cp_test_rows(manifest or self.manifest,lock,raw)

    def test_all_historical_paths_and_exact_three_test_edits_are_bound(self):
        rows=self.check(self.overlay)
        before={r['path']:r for r in self.manifest['cp']}
        after={r['path']:r for r in rows}
        self.assertEqual(len(before),1474);self.assertEqual(len(after),1476)
        self.assertEqual({p for p in after if after[p]!=before.get(p)},CP_TEST_PATHS)
        self.assertEqual(tree_hash(rows),self.lock['components']['cp']['verified_tree'])
        self.assertEqual(verify(HERE.parents[2],'cp'),1476)

    def test_rejects_runtime_overlay_missing_duplicate_extra_and_wrong_before(self):
        mutations=(lambda v:v['edits'][0].update(path='internal/httpapi/server_runtime_config.go'),
                   lambda v:v['edits'].pop(),lambda v:v['edits'].append(v['edits'][0]),
                   lambda v:v['edits'][0]['before'].update(sha256='0'*64),
                   lambda v:v.update(base_commit='0'*40),lambda v:v.update(base_product_tree='0'*40),
                   lambda v:v['edits'][1]['after'].update(mode='160000'),
                   lambda v:v['edits'][1].update(after=None),lambda v:v.update(verified_tree='0'*40))
        for mutate in mutations:
            bad=copy.deepcopy(self.overlay);mutate(bad)
            with self.subTest(value=bad),self.assertRaises((Blocked,TypeError,KeyError)):
                self.check(bad)

    def test_rejects_historical_manifest_tamper_and_raw_overlay_tamper(self):
        bad=copy.deepcopy(self.manifest);bad['cp'][0]['oid']='0'*40
        with self.assertRaises(Blocked):self.check(self.overlay,bad)
        raw=(HERE/'cp-test-overlay.json').read_bytes()+b' '
        with self.assertRaises(Blocked):cp_test_rows(self.manifest,self.lock,raw)

    def test_duplicate_json_keys_are_rejected_before_projection(self):
        with self.assertRaises(Blocked):unique_json(b'{"edits":[],"edits":[]}')


if __name__=='__main__':unittest.main()
