"""External test stream destination: TLS transport to a real FFmpeg RTMP listener."""
import os, socket, ssl, sys, threading
context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(sys.argv[1],sys.argv[2])
def forward(source,destination):
 try:
  while data:=source.recv(65536): destination.sendall(data)
 except OSError: pass
 finally:
  try: destination.shutdown(socket.SHUT_WR)
  except OSError: pass
listen_port=int(os.environ.get('UI188_EXTERNAL_TLS_PORT','1935'))
upstream_port=int(os.environ.get('UI188_EXTERNAL_RTMP_PORT','1936'))
assert (listen_port,upstream_port) in [(1935,1936),(1937,1938)], 'only the two scope-owned loopback sinks are permitted'
with socket.create_server(('127.0.0.1',listen_port)) as listener:
 print('EXTERNAL_TLS_SINK_READY',flush=True)
 while True:
  connection,_=listener.accept()
  secured=context.wrap_socket(connection,server_side=True)
  upstream=socket.create_connection(('127.0.0.1',upstream_port))
  threading.Thread(target=forward,args=(secured,upstream),daemon=True).start()
  threading.Thread(target=forward,args=(upstream,secured),daemon=True).start()
