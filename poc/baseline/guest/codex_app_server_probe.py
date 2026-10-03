"""Run inside a fresh Linux VM. No host Codex home mount or model turns."""
import json,os,queue,subprocess,threading,time
from pathlib import Path
root=Path('/data/codex-poc');work=root/'work';work.mkdir(exist_ok=True)
result={'version':'0.159.3','platform':'linux-arm64-musl','fresh_guest_home':True,'no_model_calls':True,'checks':[]}
p=subprocess.Popen([str(root/'codex-aarch64-unknown-linux-musl'),'app-server','--listen','stdio://','-c','mcp_servers={}'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=work)
q=queue.Queue();errors=[]
def read():
 for line in p.stdout:
  try:q.put(json.loads(line))
  except ValueError:pass
threading.Thread(target=read,daemon=True).start()
threading.Thread(target=lambda:errors.extend(p.stderr),daemon=True).start()
def request(rid,method,params):
 p.stdin.write(json.dumps({'id':rid,'method':method,'params':params})+'\n');p.stdin.flush();end=time.monotonic()+40
 while time.monotonic()<end:
  try:r=q.get(timeout=.25)
  except queue.Empty:
   if p.poll() is not None:raise RuntimeError('process exited '+str(p.returncode))
   continue
  if r.get('id')==rid:return r
 raise TimeoutError(method)
def check(name,fn):
 try:result['checks'].append({'name':name,'ok':True,'evidence':fn()})
 except Exception as e:result['checks'].append({'name':name,'ok':False,'error':str(e)})
def init():
 r=request(1,'initialize',{'clientInfo':{'name':'pot_linux_poc','version':'0.1'},'capabilities':{'experimentalApi':True}})
 assert 'result'in r,r
 p.stdin.write('{"method":"initialized","params":{}}\n');p.stdin.flush();return r['result']
check('initialize',init)
def duplicate():
 r=request(2,'initialize',{'clientInfo':{'name':'pot_linux_poc','version':'0.1'}});assert 'error'in r,r;return r['error']
check('duplicate_initialize_rejected',duplicate)
thread=None
def start():
 global thread
 r=request(3,'thread/start',{'cwd':str(work),'ephemeral':True,'approvalPolicy':'never','sandbox':'read-only'})
 assert 'result'in r,r;thread=r['result']['thread']['id'];return {'thread_id':thread,'ephemeral':r['result']['thread'].get('ephemeral')}
check('ephemeral_thread_start',start)
if thread:
 def read_thread():
  r=request(4,'thread/read',{'threadId':thread,'includeTurns':False});assert 'result'in r,r;assert r['result']['thread']['id']==thread;return {'same_id':True,'status':r['result']['thread'].get('status')}
 check('live_thread_read',read_thread)
 def interrupt():
  r=request(5,'turn/interrupt',{'threadId':thread,'turnId':'poc-no-such-turn'});assert 'error'in r,r;return r['error']
 check('invalid_turn_interrupt_rejected',interrupt)
if p.poll() is None:
 p.stdin.close()
 try:p.wait(5)
 except subprocess.TimeoutExpired:p.terminate();p.wait(5)
result['diagnostics']=''.join(errors)[-6000:]
(root/'app-server-results.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2))
raise SystemExit(0 if all(c['ok'] for c in result['checks']) else 1)
