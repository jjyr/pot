"""Real process/HTTP transport probes; no model prompts, secrets or global login edits."""
import json, os, queue, subprocess, threading, time, urllib.request
from pathlib import Path
ROOT=Path('/tmp/pot-research-20261003')
OUT=Path(__file__).parent/'evidence'/'engine-probes.json'
class Peer:
 def __init__(self,cmd,env=None):
  self.p=subprocess.Popen(cmd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=env)
  self.q=queue.Queue();self.stderr=[]
  def read():
   for line in self.p.stdout:
    try:self.q.put(json.loads(line))
    except ValueError:pass
  def err():
   for line in self.p.stderr:self.stderr.append(line)
  threading.Thread(target=read,daemon=True).start();threading.Thread(target=err,daemon=True).start()
 def send(self,msg):self.p.stdin.write(json.dumps(msg)+'\n');self.p.stdin.flush()
 def request(self,msg,matcher=None,timeout=65):
  self.send(msg);end=time.monotonic()+timeout
  while time.monotonic()<end:
   try:r=self.q.get(timeout=max(.01,end-time.monotonic()))
   except queue.Empty:break
   if (matcher(r) if matcher else r.get('id')==msg.get('id')):return r
  raise TimeoutError('No matching response; process exit='+str(self.p.poll()))
 def close(self):
  if self.stderr:
   safe=[x for x in self.stderr if any(k in x for k in ('state runtime','state db backfill','state runtime','WARNING: proceeding'))]
   (ROOT/'logs'/('peer-'+str(self.p.pid)+'.txt')).write_text(''.join(safe))
  if self.p.poll() is None:
   self.p.terminate()
   try:self.p.wait(5)
   except subprocess.TimeoutExpired:self.p.kill();self.p.wait()
rows=[]
def check(engine,name,fn):
 try:rows.append({'engine':engine,'probe':name,'ok':True,'evidence':fn()})
 except Exception as e:rows.append({'engine':engine,'probe':name,'ok':False,'error':str(e)})
# Codex may be excluded while the host backfill is busy.
# Codex process inherits existing home for read-only configuration; no turn/model call.
if os.environ.get('POT_SKIP_CODEX') == '1':
 if OUT.exists():
  rows.extend(r for r in json.loads(OUT.read_text())['results'] if r['engine']=='codex')
else:
 p=None
 try:
  p=Peer(['codex','app-server','--listen','stdio://','-c','mcp_servers={}','-c','sqlite_home="/tmp/pot-research-20261003/homes/codex-sqlite"'])
  def init():
   r=p.request({'id':1,'method':'initialize','params':{'clientInfo':{'name':'pot_poc','version':'0.1.0'},'capabilities':{'experimentalApi':True}}})
   assert 'result' in r,r.get('error');p.send({'method':'initialized','params':{}})
   return {'response_keys':list(r['result'])}
  check('codex','initialize',init)
  def duplicate():
   r=p.request({'id':2,'method':'initialize','params':{'clientInfo':{'name':'pot_poc','version':'0.1.0'}}});assert 'error'in r
   return {'error_code':r['error']['code'],'message':r['error']['message']}
  check('codex','duplicate_initialize_rejected',duplicate)
  def start():
   r=p.request({'id':3,'method':'thread/start','params':{'cwd':str(ROOT),'ephemeral':True,'approvalPolicy':'never','sandbox':'read-only'}})
   if 'error'in r:raise RuntimeError(r['error']['message'])
   return {'thread_id':r['result']['thread']['id'],'ephemeral':True,'response_keys':list(r['result'])}
  check('codex','ephemeral_thread_start_no_inference',start)
 finally:
  if p:p.close()
# Pi isolated home, no extensions, no credentials, no startup network.
env=os.environ.copy();env.update(PI_CODING_AGENT_DIR=str(ROOT/'homes/pi'),PI_OFFLINE='1',PI_TELEMETRY='0')
p=None
try:
 p=Peer(['pi','--mode','rpc','--no-extensions','--no-skills','--no-prompt-templates','--no-context-files','--session-dir',str(ROOT/'homes/pi/sessions')],env)
 state=p.request({'id':'p1','type':'get_state'});assert state['success'];path=state['data'].get('sessionFile')
 rows.append({'engine':'pi','probe':'get_state','ok':True,'evidence':{'session_file':path,'keys':list(state['data'])}})
 for i,command in enumerate(('get_messages','get_session_stats','abort','new_session')):
  def run(command=command,i=i):
   r=p.request({'id':f'p{i+2}','type':command});assert r['success'],r
   return {'command':r['command'],'data_keys':list(r.get('data',{})) if isinstance(r.get('data'),dict) else type(r.get('data')).__name__}
  check('pi',command,run)
 def name_session():
  r=p.request({'id':'p7','type':'set_session_name','name':'Pot persistence POC'});assert r['success'],r
  state=p.request({'id':'p8','type':'get_state'});return state['data'].get('sessionFile')
 check('pi','set_session_name_no_turn',lambda:{'session_path':name_session()})
 state=p.request({'id':'p9','type':'get_state'});path=state['data'].get('sessionFile')
 if path and Path(path).exists():
  def switch():
   p.request({'id':'p10','type':'new_session'})
   r=p.request({'id':'p11','type':'switch_session','sessionPath':path});assert r['success'],r
   return {'session_path':path}
  check('pi','switch_session',switch)
finally:
 if p:p.close()
# OpenCode isolated server started by runner; HTTP configuration/session lifecycle only.
base='http://127.0.0.1:17409'
def http(method,path,body=None):
 data=json.dumps(body).encode() if body is not None else None
 req=urllib.request.Request(base+path,data=data,method=method,headers={'Content-Type':'application/json'})
 with urllib.request.urlopen(req,timeout=15) as r:return json.load(r)
check('opencode','health',lambda:http('GET','/global/health'))
def oc():
 s=http('POST','/session',{'title':'Pot transport POC'});sid=s['id']
 read=http('GET','/session/'+sid);assert read['id']==sid
 aborted=http('POST','/session/'+sid+'/abort',{})
 deleted=http('DELETE','/session/'+sid)
 return {'session_id':sid,'abort':aborted,'delete':deleted}
check('opencode','create_read_abort_delete_session',oc)
check('opencode','openapi_available',lambda:{'openapi':http('GET','/doc')['openapi'],'path_count':len(http('GET','/doc')['paths'])})
# Claude SDK CLI control handshake only; empty isolated config. No anthropic request.
cli=ROOT/'claude-sdk/node_modules/@anthropic-ai/claude-agent-sdk-darwin-arm64/claude'
if cli.exists():
 env=os.environ.copy();env['CLAUDE_CONFIG_DIR']=str(ROOT/'homes/claude-sdk');env['CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC']='1'
 for key in list(env):
  if any(x in key for x in ('API_KEY','AUTH_TOKEN','OAUTH_TOKEN')):env.pop(key)
 p=None
 try:
  p=Peer([str(cli),'-p','--input-format','stream-json','--output-format','stream-json','--verbose','--strict-mcp-config','--mcp-config','{"mcpServers":{}}'],env)
  def ci():
   r=p.request({'type':'control_request','request_id':'ci','request':{'subtype':'initialize'}},lambda r:r.get('type')=='control_response' and r.get('response',{}).get('request_id')=='ci')
   assert r['response']['subtype']=='success',r
   data=r['response'].get('response',{})
   return {'keys':list(data),'model_count':len(data.get('models',[]))}
  check('claude','sdk_initialize_without_inference',ci)
 finally:
  if p:p.close()
else:rows.append({'engine':'claude','probe':'sdk_initialize_without_inference','ok':False,'error':'SDK not installed'})
OUT.write_text(json.dumps({'no_model_calls':True,'results':rows},indent=2))
for r in rows:print(r['engine'],r['probe'],'PASS' if r['ok'] else 'FAIL',r.get('error',''))
