"""Deterministic product-runtime POC. No claim of semantic LLM memory or hard OS isolation."""
import json, sqlite3, threading, time, uuid
from contextlib import contextmanager

class Runtime:
 def __init__(self,path):
  self.path=str(path);self.db=sqlite3.connect(self.path,timeout=10,check_same_thread=False)
  self.db.execute('PRAGMA journal_mode=WAL');self.db.execute('PRAGMA foreign_keys=ON')
  self.db.executescript('''
  CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT);
  CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,conversation TEXT,environment TEXT,status TEXT,generation INTEGER,goal TEXT);
  CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,conversation TEXT,text TEXT,status TEXT);
  CREATE TABLE IF NOT EXISTS outbox(id TEXT PRIMARY KEY,task TEXT,payload TEXT,delivered INTEGER DEFAULT 0);
  CREATE TABLE IF NOT EXISTS operations(id TEXT PRIMARY KEY,task TEXT,status TEXT,external_id TEXT);
  CREATE TABLE IF NOT EXISTS facts(id TEXT PRIMARY KEY,claim TEXT,status TEXT,version INTEGER);
  CREATE TABLE IF NOT EXISTS suppressions(id TEXT PRIMARY KEY);
  CREATE TABLE IF NOT EXISTS bindings(task TEXT PRIMARY KEY,engine TEXT,thread TEXT,generation INTEGER,memory_version INTEGER);
  CREATE TABLE IF NOT EXISTS schedules(id TEXT PRIMARY KEY,task TEXT,enabled INTEGER,next_at INTEGER,interval INTEGER,policy TEXT);
  CREATE TABLE IF NOT EXISTS schedule_runs(schedule TEXT,scheduled_at INTEGER,run_id TEXT,UNIQUE(schedule,scheduled_at));
  CREATE TABLE IF NOT EXISTS grants(id TEXT PRIMARY KEY,account TEXT,scope TEXT,revoked INTEGER DEFAULT 0);
  ''')
  self.db.execute("INSERT OR IGNORE INTO settings VALUES ('name','Pot')");self.db.commit()
 def close(self):self.db.close()
 def settings(self,key,value=None):
  if value is not None:
   with self.db:self.db.execute('INSERT OR REPLACE INTO settings VALUES (?,?)',(key,value))
  r=self.db.execute('SELECT value FROM settings WHERE key=?',(key,)).fetchone();return r[0] if r else None
 def accept(self,request,conversation,text):
  with self.db:self.db.execute('INSERT OR IGNORE INTO messages VALUES (?,?,?,?)',(request,conversation,text,'accepted'))
 def apply_input(self,request):
  with self.db:self.db.execute("UPDATE messages SET status='applied' WHERE id=?",(request,))
 def task(self,goal,environment='local',conversation=None):
  tid=uuid.uuid4().hex;conversation=conversation or 'task:'+tid
  with self.db:self.db.execute('INSERT INTO tasks VALUES (?,?,?,?,?,?)',(tid,conversation,environment,'queued',1,goal))
  return tid
 def transition(self,task,status,generation):
  with self.db:
   row=self.db.execute('SELECT generation,status FROM tasks WHERE id=?',(task,)).fetchone()
   if not row or row[0]!=generation or row[1]in('cancelled','succeeded'):raise ValueError('stale or terminal run')
   self.db.execute('UPDATE tasks SET status=? WHERE id=?',(status,task))
   self.db.execute('INSERT INTO outbox(id,task,payload) VALUES (?,?,?)',(uuid.uuid4().hex,task,json.dumps({'status':status,'generation':generation})))
 def cancel(self,task):
  with self.db:
   self.db.execute("UPDATE tasks SET status='cancelled',generation=generation+1 WHERE id=?",(task,))
   self.db.execute('INSERT INTO outbox(id,task,payload) VALUES (?,?,?)',(uuid.uuid4().hex,task,'{"status":"cancelled"}'))
 def recover(self):
  # Never repeat an operation whose effect was not confirmed before the crash.
  with self.db:
   self.db.execute("UPDATE operations SET status='unknown' WHERE status='inflight'")
   self.db.execute("UPDATE tasks SET status='waiting_user',generation=generation+1 WHERE status='running'")
 def begin_operation(self,oid,task):
  with self.db:self.db.execute("INSERT INTO operations VALUES (?,?,'inflight',NULL)",(oid,task))
 def resolve(self,oid,external_id):
  with self.db:self.db.execute("UPDATE operations SET status='succeeded',external_id=? WHERE id=?",(external_id,oid))
 def pending(self):return self.db.execute('SELECT id,task,payload FROM outbox WHERE delivered=0 ORDER BY rowid').fetchall()
 def ack(self,event):
  with self.db:self.db.execute('UPDATE outbox SET delivered=1 WHERE id=?',(event,))
 def fact(self,fid,claim,explicit=False):
  if not explicit and self.db.execute('SELECT 1 FROM suppressions WHERE id=?',(fid,)).fetchone():return False
  with self.db:
   version=self.memory_version()+1
   self.db.execute('INSERT INTO facts VALUES (?,?,?,?) ON CONFLICT(id) DO UPDATE SET claim=excluded.claim,status=excluded.status,version=excluded.version',(fid,claim,'active',version))
   self.db.execute("INSERT OR REPLACE INTO settings VALUES ('memory_version',?)",(str(version),))
   if explicit:self.db.execute('DELETE FROM suppressions WHERE id=?',(fid,))
  return True
 def forget(self,fid):
  with self.db:
   version=self.memory_version()+1
   self.db.execute("UPDATE facts SET claim='',status='deleted',version=? WHERE id=?",(version,fid))
   self.db.execute('INSERT OR IGNORE INTO suppressions VALUES (?)',(fid,))
   self.db.execute("INSERT OR REPLACE INTO settings VALUES ('memory_version',?)",(str(version),))
  # Does not delete provider-native copies; invalidates their active bindings.
 def memory_version(self):return int(self.settings('memory_version') or 0)
 def context(self):return [r[0] for r in self.db.execute("SELECT claim FROM facts WHERE status='active'")]
 def bind(self,task,engine,thread):
  g=self.db.execute('SELECT generation FROM tasks WHERE id=?',(task,)).fetchone()[0]
  with self.db:self.db.execute('INSERT OR REPLACE INTO bindings VALUES (?,?,?,?,?)',(task,engine,thread,g,self.memory_version()))
 def binding_valid(self,task):
  r=self.db.execute('SELECT b.generation,b.memory_version,t.generation FROM bindings b JOIN tasks t ON b.task=t.id WHERE b.task=?',(task,)).fetchone()
  return bool(r and r[0]==r[2] and r[1]==self.memory_version())
 def schedule(self,sid,task,next_at,interval,policy='coalesce'):
  with self.db:self.db.execute('INSERT INTO schedules VALUES (?,?,1,?,?,?)',(sid,task,next_at,interval,policy))
 def tick(self,now):
  created=[]
  with self.db:
   for sid,task,due,interval,policy in self.db.execute('SELECT id,task,next_at,interval,policy FROM schedules WHERE enabled=1 AND next_at<=?',(now,)).fetchall():
    if interval<=0:raise ValueError('invalid interval')
    next_at=due+((now-due)//interval+1)*interval
    if policy!='skip':
     rid=uuid.uuid4().hex
     self.db.execute('INSERT OR IGNORE INTO schedule_runs VALUES (?,?,?)',(sid,due,rid));created.append((sid,rid))
    self.db.execute('UPDATE schedules SET next_at=? WHERE id=?',(next_at,sid))
  return created
 def disable_schedule(self,sid):
  with self.db:self.db.execute('UPDATE schedules SET enabled=0 WHERE id=?',(sid,))
 def grant(self,gid,account,scope):
  with self.db:self.db.execute('INSERT INTO grants VALUES (?,?,?,0)',(gid,account,scope))
 def revoke(self,gid):
  with self.db:self.db.execute('UPDATE grants SET revoked=1 WHERE id=?',(gid,))
 def review(self,gid,account,scope,action,rule,explicit=False,research=False):
  # Unrecognized values must never grant an external action.
  if rule not in {'allow','ask','explicit','handoff'}:return 'deny'
  if action not in {'read','draft','send','delete','desktop','change_password'}:return 'deny'
  row=self.db.execute('SELECT account,scope,revoked FROM grants WHERE id=?',(gid,)).fetchone()
  if not row or row!=(account,scope,0):return 'deny'
  if research and action!='read':return 'deny'
  if action=='change_password' or rule=='handoff':return 'handoff'
  if rule=='ask':return 'ask'
  if rule=='explicit' and not explicit:return 'ask'
  if action=='send' and not explicit and rule!='allow':return 'ask'
  return 'allow'

class DesktopGate:
 """Cooperative broker only; arbitrary shell access MUST be denied by OS boundary separately."""
 def __init__(self,store):
  self.store=store;self.cv=threading.Condition();self.active=0
  self.enabled=store.settings('desktop_enabled')!='false';self.generation=0
 @contextmanager
 def agent_input(self):
  with self.cv:
   if not self.enabled:raise PermissionError('human owns desktop')
   self.active+=1;generation=self.generation
  try:yield generation
  finally:
   with self.cv:self.active-=1;self.cv.notify_all()
 def take_over(self,timeout=1):
  with self.cv:
   self.enabled=False;self.generation+=1;self.store.settings('desktop_enabled','false')
   end=time.monotonic()+timeout
   while self.active:
    remaining=end-time.monotonic()
    if remaining<=0:return False
    self.cv.wait(remaining)
   return True
 def return_control(self):
  with self.cv:
   if self.active:raise RuntimeError('takeover has not drained in-flight input')
   self.enabled=True;self.generation+=1;self.store.settings('desktop_enabled','true')
  return 'reobserve_required'
 def human_input(self):return not self.enabled and self.active==0
