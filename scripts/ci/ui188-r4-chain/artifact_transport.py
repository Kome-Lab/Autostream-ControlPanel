"""Owned loopback forwarding observer. No internal response/media fabrication.

The real Encoder request is held verbatim until explicit finite release. Only
the actual upstream CP status/body can complete the original service request.
"""
import hashlib,http.client,http.server,json,os,pathlib,threading,time
class Transport:
    def __init__(self,evidence,case):
        self.evidence=pathlib.Path(evidence);self.case=case;self.lock=threading.Lock();self.reports={};self.calls=[];self.servers=[]
    def save(self):
        with self.lock:
            value={'reports':{k:{x:y for x,y in v.items() if x!='release'} for k,v in self.reports.items()},'control_calls':list(self.calls)}
            (self.evidence/('r9-transport-'+self.case+'.json')).write_text(json.dumps(value,indent=2)+'\n')
        return value
    def start(self):
        for source,target in [(18841,18842),*[(p,p+10000) for p in range(18081,18087)]]:
            owner=self
            class Handler(http.server.BaseHTTPRequestHandler):
                def log_message(self,*args):pass
                def handle_request(self):
                    size=int(self.headers.get('Content-Length','0'));assert 0<=size<=32*1024*1024
                    body=self.rfile.read(size);port=self.server.target
                    if self.path=='/r9/transport' and self.command=='GET':
                        data=json.dumps(owner.save()).encode();self.send_response(200);self.end_headers();self.wfile.write(data);return
                    if self.path=='/r9/release' and self.command=='POST':
                        value=json.loads(body);assert set(value)=={'service_id'} and value['service_id'] in ['encoder-a','encoder-b']
                        with owner.lock:record=owner.reports[value['service_id']];record['release'].set()
                        self.send_response(204);self.end_headers();return
                    report=None
                    if port==18842 and self.command=='POST' and self.path=='/services/stream-artifacts':
                        value=json.loads(body);service=value['service_id'];assert service in ['encoder-a','encoder-b']
                        report={'service_id':service,'stream_id':value['stream_id'],'archive_run_id':value['archive_run_id'],'archive_started_at':value['archive_started_at'],'artifact_count':len(value['artifacts']),'body_sha256':hashlib.sha256(body).hexdigest(),'arrived_at':time.time(),'release':threading.Event()}
                        with owner.lock:
                            assert service not in owner.reports,'unconditional report retry prohibited in test'
                            owner.reports[service]=report
                        owner.save()
                        assert report['release'].wait(25),'finite real artifact report barrier expired'
                    control=None
                    if self.command=='POST' and ((port!=18842 and any(part in self.path for part in ['/start','/prepare','/commit'])) or (port==18842 and self.path.startswith('/streams/') and self.path.endswith('/start'))):
                        value=json.loads(body)
                        control={'at':time.time(),'port':self.server.server_port,'stage':'CP-start-ingress' if port==18842 else 'service-ingress','method':self.command,'path':self.path,'stream_id':self.path.split('/')[2] if port==18842 else value.get('stream_id',value.get('id')),'body_sha256':hashlib.sha256(body).hexdigest()}
                        with owner.lock:owner.calls.append(control)
                        owner.save()
                    headers={k:v for k,v in self.headers.items() if k.lower() not in ['connection','proxy-connection']}
                    connection=http.client.HTTPConnection('127.0.0.1',port,timeout=60)
                    try:
                        connection.request(self.command,self.path,body=body if size else None,headers=headers)
                        response=connection.getresponse();data=response.read()
                        if control is not None:
                            with owner.lock:control.update({'actual_upstream_status':response.status,'completed_at':time.time()})
                            owner.save()
                        if report is not None:
                            with owner.lock:report.update({'forwarded_body_sha256':hashlib.sha256(body).hexdigest(),'actual_upstream_status':response.status,'upstream_body_sha256':hashlib.sha256(data).hexdigest(),'completed_at':time.time()})
                            owner.save()
                        self.send_response(response.status)
                        for k,v in response.getheaders():
                            if k.lower() not in ['transfer-encoding','connection','content-length']:self.send_header(k,v)
                        self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data);self.wfile.flush()
                        if report is not None:
                            with owner.lock:report.update({'client_response_delivered':True,'client_response_written_at':time.time()})
                            owner.save()
                    except Exception as error:
                        if report is not None:
                            with owner.lock:report.update({'client_response_delivered':False,'client_response_error_type':type(error).__name__})
                            owner.save()
                        raise
                    finally:connection.close()
                do_GET=do_POST=do_PUT=do_DELETE=do_PATCH=handle_request
            server=http.server.ThreadingHTTPServer(('0.0.0.0' if source==18841 else '127.0.0.1',source),Handler);server.target=target;self.servers.append(server)
            threading.Thread(target=server.serve_forever,daemon=True).start()
        return self
    def close(self):
        for server in self.servers:server.shutdown();server.server_close()
if __name__=='__main__':
    transport=Transport(os.environ['UI188_EVIDENCE_DIRECTORY'],os.environ['UI188_PROBE_ATTEMPT']).start()
    try:
        while True:time.sleep(1)
    finally:transport.close()
