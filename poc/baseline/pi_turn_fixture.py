"""Real Pi tool loop/session/active abort using a deterministic loopback model fixture.
No real model inference, credentials or cloud calls. Not a model quality evaluation.
"""
import json,os,queue,subprocess,tempfile,threading,time
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path
class Model(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_POST(self):
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  messages=body.get('messages',[]);self.server.requests.append({'path':self.path,'roles':[m['role'] for m in messages],'steering_marker':any('STEER_DELIVERED_MARKER' in str(m.get('content')) for m in messages)})
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  def emit(delta,finish=None):
   chunk={'id':'poc-chat','object':'chat.completion.chunk','created':1,'model':'fixture','choices':[{'index':0,'delta':delta,'finish_reason':finish}]}
   self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode());self.wfile.flush()
  try:
   emit({'role':'assistant'})
   if any(m.get('role')=='user' and 'CANCEL_POC' in str(m.get('content')) for m in messages):
    self.server.blocked.set();emit({'content':'waiting'})
    self.server.release.wait(20);emit({},'stop')
   elif any(m.get('role')=='tool' for m in messages):
    emit({'content':'FIXTURE_TOOL_DONE'});emit({},'stop')
   else:
    if any('STEER_POC' in str(m.get('content')) for m in messages):
     self.server.blocked.set();self.server.release.wait(15)
    emit({'tool_calls':[{'index':0,'id':'call_poc_write','type':'function','function':{'name':'write','arguments':json.dumps({'path':'proof.txt','content':'written-by-real-pi-tool\n'})}}]});emit({},'tool_calls')
   self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()
  except (BrokenPipeError,ConnectionResetError):pass
class Peer:
 def __init__(self,args,env,cwd):
  self.p=subprocess.Popen(args,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=env,cwd=cwd)
  self.records=[];self.cv=threading.Condition();self.errors=[]
  def reader():
   for line in self.p.stdout:
    try:r=json.loads(line)
    except ValueError:continue
    with self.cv:self.records.append(r);self.cv.notify_all()
  threading.Thread(target=reader,daemon=True).start()
  threading.Thread(target=lambda:self.errors.extend(self.p.stderr),daemon=True).start()
 def send(self,record):
  self.p.stdin.write(json.dumps(record)+'\n');self.p.stdin.flush()
 def wait(self,predicate,start=0,timeout=15):
  end=time.monotonic()+timeout
  with self.cv:
   while True:
    for r in self.records[start:]:
     if predicate(r):return r
    remaining=end-time.monotonic()
    if remaining<=0:raise TimeoutError('Pi event deadline: '+str([r.get('type') for r in self.records[start:]]))
    self.cv.wait(remaining)
 def command(self,typ,**kwargs):
  rid=str(time.monotonic_ns());self.send({'id':rid,'type':typ,**kwargs});r=self.wait(lambda r:r.get('id')==rid);assert r.get('success'),r;return r
 def close(self):
  if self.p.poll() is None:
   self.p.stdin.close()
   try:self.p.wait(3)
   except subprocess.TimeoutExpired:self.p.terminate();self.p.wait(3)
def main():
 out={'classification':'real Pi with deterministic model fixture','real_model_inference':False,'checks':{}}
 server=ThreadingHTTPServer(('127.0.0.1',0),Model);server.requests=[];server.blocked=threading.Event();server.release=threading.Event()
 threading.Thread(target=server.serve_forever,daemon=True).start();peer=None
 try:
  with tempfile.TemporaryDirectory(prefix='pot-pi-turn-') as temp:
   root=Path(temp);config=root/'config';config.mkdir();work=root/'work';work.mkdir()
   (config/'models.json').write_text(json.dumps({'providers':{'pot-fixture':{'baseUrl':f'http://127.0.0.1:{server.server_port}/v1','api':'openai-completions','apiKey':'fixture-no-real-key','models':[{'id':'fixture','reasoning':False,'input':['text'],'contextWindow':16384,'maxTokens':1024,'cost':{'input':0,'output':0,'cacheRead':0,'cacheWrite':0}}]}}}))
   (config/'settings.json').write_text(json.dumps({'retry':{'enabled':False}}))
   env=os.environ.copy();env.update(PI_CODING_AGENT_DIR=str(config),PI_OFFLINE='1',PI_TELEMETRY='0',NO_PROXY='127.0.0.1,localhost')
   for key in list(env):
    if any(x in key for x in ('API_KEY','AUTH_TOKEN','OAUTH_TOKEN')):env.pop(key)
   args=['pi','--mode','rpc','--provider','pot-fixture','--model','fixture','--thinking','off','--tools','write,read','--no-extensions','--no-skills','--no-prompt-templates','--no-context-files','--session-dir',str(root/'sessions')]
   peer=Peer(args,env,work);peer.command('get_state')
   start=len(peer.records);peer.command('prompt',message='Write the POC proof file.');peer.wait(lambda r:r.get('type')=='agent_settled',start)
   assert (work/'proof.txt').read_text()=='written-by-real-pi-tool\n'
   assert any(r.get('type')=='tool_execution_end' and not r.get('isError') for r in peer.records)
   out['checks']['real_write_tool_and_settled']=True
   state=peer.command('get_state')['data'];path=state['sessionFile'];assert Path(path).is_file()
   messages=peer.command('get_messages')['data']['messages'];assert any(m.get('role')=='toolResult' for m in messages)
   peer.close();peer=Peer(args+['--session',path],env,work)
   restored=peer.command('get_messages')['data']['messages'];assert restored==messages
   out['checks']['session_file_and_process_restart_resume']=True;out['restored_message_count']=len(restored)
   start=len(peer.records);peer.command('prompt',message='CANCEL_POC');assert server.blocked.wait(5)
   peer.command('abort');peer.wait(lambda r:r.get('type')=='agent_settled',start)
   state=peer.command('get_state')['data'];assert not state['isStreaming'];out['checks']['active_stream_abort_and_idle']=True
   peer.command('new_session');server.blocked.clear();server.release.clear()
   start=len(peer.records);peer.command('prompt',message='STEER_POC');assert server.blocked.wait(5)
   peer.command('steer',message='STEER_DELIVERED_MARKER');server.release.set()
   peer.wait(lambda r:r.get('type')=='agent_settled',start)
   assert any(r['steering_marker'] for r in server.requests)
   out['checks']['active_steer_received_by_next_model_request']=True
   out['event_types']=sorted({r.get('type','') for r in peer.records});out['fixture_requests']=server.requests
 except Exception as e:out['error']=type(e).__name__+': '+str(e)
 finally:
  server.release.set()
  if peer:peer.close()
  server.shutdown();server.server_close()
  Path(__file__).parent.joinpath('evidence/pi-turn-fixture.json').write_text(json.dumps(out,indent=2));print(json.dumps(out,indent=2))
 return 'error' not in out
if __name__=='__main__':raise SystemExit(0 if main() else 1)
