"""Finite wire-shape oracle only; actual report/DB proof belongs to B5."""
import unittest
from archive_barrier import rows,verify_catalog_authority
class CatalogWireShape(unittest.TestCase):
    def test_only_two_empty_wire_representations(self):
        self.assertEqual(rows(None),[]);self.assertEqual(rows([]),[])
        for value in [{},'',0,False,True,{'artifacts':[]}]:
            with self.subTest(value=value),self.assertRaisesRegex(AssertionError,'must be null or array'):rows(value)
    def test_nonempty_array_is_never_erased(self):
        value=[{'kind':'archive'}];self.assertIs(rows(value),value);self.assertNotEqual(rows(value),[])
    def fixture(self):
        current={'id':'stream-1','archive_run_id':'run-1','archive_started_at':'2026-10-15T16:00:00.123456Z'}
        api=[{**current,'id':kind,'stream_id':'stream-1','kind':kind} for kind in ['archive','metadata','logs']]
        db=[{**r,'source_service_id':'encoder-b','archive_started_at':'2026-10-15 16:00:00.123456'} for r in api]
        return api,current,db
    def test_private_owner_is_verified_in_actual_db_projection(self):
        api,current,db=self.fixture();self.assertTrue(all('source_service_id' not in r for r in api))
        verify_catalog_authority(api,current,db,'encoder-b')
    def test_owner_run_start_id_partial_and_kind_fail_closed(self):
        for field,bad in [('source_service_id','encoder-other'),('archive_run_id','old-run'),('archive_started_at','2026-10-15 15:59:00.123456'),('stream_id','other-stream'),('id','other-id'),('kind','other-kind')]:
            api,current,db=self.fixture();db[0][field]=bad
            with self.subTest(field=field),self.assertRaises(AssertionError):verify_catalog_authority(api,current,db,'encoder-b')
        api,current,db=self.fixture()
        with self.assertRaisesRegex(AssertionError,'denominator'):verify_catalog_authority(api,current,db[:2],'encoder-b')
if __name__=='__main__':unittest.main(verbosity=2)
