"""Unit-only transport oracle; its fixture responses are never B5 receipts."""
import hashlib,http.client,http.server,json,tempfile,threading,unittest
from artifact_transport import Transport

class VerbatimTransportTest(unittest.TestCase):
    def test_actual_request_waits_then_forwards_verbatim_and_preserves_upstream_failure(self):
        received=[]
        class Origin(http.server.BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_POST(self):
                body=self.rfile.read(int(self.headers.get('Content-Length','0')))
                received.append((self.path,body,self.headers.get('Authorization')))
                self.send_response(202);self.end_headers();self.wfile.write(b'{"unit_origin":"accepted"}')
            def do_GET(self):
                self.send_response(503);self.end_headers();self.wfile.write(b'{"unit_origin":"unavailable"}')
        origin=http.server.ThreadingHTTPServer(('127.0.0.1',18842),Origin)
        thread=threading.Thread(target=origin.serve_forever,daemon=True);thread.start()
        with tempfile.TemporaryDirectory() as directory:
            transport=Transport(directory,'unit-only').start()
            try:
                payload=json.dumps({'service_id':'encoder-b','stream_id':'unit-stream','archive_run_id':'unit-run','archive_started_at':'2026-10-15T14:00:00Z','artifacts':[{'kind':kind} for kind in ['archive','metadata','logs']]},separators=(',',':')).encode()
                done=threading.Event();result=[]
                def producer():
                    c=http.client.HTTPConnection('127.0.0.1',18841,timeout=10)
                    try:
                        c.request('POST','/services/stream-artifacts',body=payload,headers={'Authorization':'Bearer UNIT-SECRET-NOT-EVIDENCE','Content-Type':'application/json'})
                        r=c.getresponse();result.append((r.status,r.read()))
                    finally:c.close();done.set()
                producer_thread=threading.Thread(target=producer,daemon=True);producer_thread.start()
                import time
                deadline=time.monotonic()+3
                while 'encoder-b' not in transport.save()['reports']:
                    self.assertLess(time.monotonic(),deadline);time.sleep(.01)
                self.assertFalse(done.is_set());self.assertEqual(received,[])
                captured=transport.save()['reports']['encoder-b'];self.assertEqual(captured['body_sha256'],hashlib.sha256(payload).hexdigest())
                c=http.client.HTTPConnection('127.0.0.1',18841,timeout=5)
                c.request('POST','/r9/release',body=b'{"service_id":"encoder-b"}');r=c.getresponse();self.assertEqual(r.status,204);r.read();c.close()
                self.assertTrue(done.wait(5));producer_thread.join()
                self.assertEqual(received,[('/services/stream-artifacts',payload,'Bearer UNIT-SECRET-NOT-EVIDENCE')])
                self.assertEqual(result,[(202,b'{"unit_origin":"accepted"}')])
                proof=transport.save();self.assertEqual(proof['reports']['encoder-b']['actual_upstream_status'],202)
                self.assertEqual(proof['reports']['encoder-b']['forwarded_body_sha256'],hashlib.sha256(payload).hexdigest())
                self.assertNotIn('UNIT-SECRET-NOT-EVIDENCE',json.dumps(proof))
                c=http.client.HTTPConnection('127.0.0.1',18841,timeout=5);c.request('GET','/unit-failure');r=c.getresponse()
                self.assertEqual((r.status,r.read()),(503,b'{"unit_origin":"unavailable"}'));c.close()
            finally:transport.close()
        origin.shutdown();origin.server_close();thread.join()

if __name__=='__main__':unittest.main(verbosity=2)
