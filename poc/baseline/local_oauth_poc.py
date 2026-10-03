"""Loopback callback and credential-boundary fixture; NOT GitHub's live OAuth service."""
import base64,hashlib,hmac,json,secrets,time,threading,unittest,urllib.request,urllib.error
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from urllib.parse import parse_qs,urlparse,urlencode
class Flow:
 def __init__(self):self.sessions={};self.grants={};self.lock=threading.Lock()
 def begin(self,account,scope,ttl=30):
  state=secrets.token_urlsafe(32);verifier=secrets.token_urlsafe(48)
  challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
  self.sessions[state]={'account':account,'scope':scope,'deadline':time.monotonic()+ttl,'challenge':challenge,'verifier':verifier}
  return state,challenge
 def callback(self,state,code,challenge):
  with self.lock:
   s=self.sessions.pop(state,None)
   if not s or s['deadline']<time.monotonic():raise ValueError('invalid/expired/replayed state')
   if not hmac.compare_digest(s['challenge'],challenge):raise ValueError('PKCE mismatch')
   if code!='fixture-approved-code':raise ValueError('exchange rejected')
   gid=secrets.token_hex(8);self.grants[gid]={'account':s['account'],'scope':s['scope'],'secret_ref':'vault://fixture/'+gid,'revoked':False}
   return gid
 def call(self,gid,account,scope):
  g=self.grants[gid]
  if g['revoked'] or (g['account'],g['scope'])!=(account,scope):raise PermissionError()
  return {'account':g['account'],'scope':g['scope'],'result':'fixture-read-success'}
 def revoke(self,gid):self.grants[gid]['revoked']=True
flow=Flow()
class Callback(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  q=parse_qs(urlparse(self.path).query)
  try:gid=flow.callback(q.get('state',[''])[0],q.get('code',[''])[0],q.get('challenge',[''])[0]);body={'grant_id':gid};code=200
  except ValueError as e:body={'error':str(e)};code=400
  b=json.dumps(body).encode();self.send_response(code);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(b)));self.end_headers();self.wfile.write(b)
class OAuthTests(unittest.TestCase):
 @classmethod
 def setUpClass(cls):
  cls.server=ThreadingHTTPServer(('127.0.0.1',0),Callback);threading.Thread(target=cls.server.serve_forever,daemon=True).start();cls.base='http://127.0.0.1:'+str(cls.server.server_port)
 @classmethod
 def tearDownClass(cls):cls.server.shutdown();cls.server.server_close()
 def request(self,state,challenge):
  url=self.base+'/callback?'+urlencode({'state':state,'challenge':challenge,'code':'fixture-approved-code'})
  with urllib.request.urlopen(url) as r:return json.load(r)
 def test_actual_loopback_callback_single_use_and_revoke(self):
  state,c=flow.begin('github-fixture-a','repo-read');gid=self.request(state,c)['grant_id'];self.assertEqual(flow.call(gid,'github-fixture-a','repo-read')['result'],'fixture-read-success')
  with self.assertRaises(urllib.error.HTTPError):self.request(state,c)
  flow.revoke(gid)
  with self.assertRaises(PermissionError):flow.call(gid,'github-fixture-a','repo-read')
 def test_wrong_state_pkce_expiry(self):
  with self.assertRaises(urllib.error.HTTPError):self.request('foreign-state','foreign-challenge')
  state,c=flow.begin('github-fixture-a','repo-read')
  with self.assertRaises(urllib.error.HTTPError):self.request(state,'mismatch')
  state,c=flow.begin('github-fixture-a','repo-read',ttl=-1)
  with self.assertRaises(urllib.error.HTTPError):self.request(state,c)
 def test_account_scope_cannot_be_changed_by_callback(self):
  state,c=flow.begin('github-fixture-a','repo-read');gid=self.request(state,c)['grant_id']
  with self.assertRaises(PermissionError):flow.call(gid,'github-fixture-b','repo-read')
  with self.assertRaises(PermissionError):flow.call(gid,'github-fixture-a','repo-write')
  self.assertNotIn('token',flow.call(gid,'github-fixture-a','repo-read'))
if __name__=='__main__':unittest.main(verbosity=2)
