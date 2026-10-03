"""Real helper/VM fault checks with a minimal Alpine workload, not desktop acceptance."""
import json,os,pathlib,signal,socket,subprocess,time,uuid,shutil
base=pathlib.Path('.local/poc').resolve();root=base/'fault-runtime';res=base/'fault-resources';exe=base/'pot Local POC.app/Contents/Helpers/PotHelper';result={}
root.mkdir(parents=True,exist_ok=True);res.mkdir(parents=True,exist_ok=True)
for name in ['vmlinux','novnc.tar.gz','test.html','packages.lock','broker.py','driver.py','index.html']:
 shutil.copy2(base/'local-resources'/name,res/name)
shutil.copy2(pathlib.Path(__file__).parent/'probe.sh',res/'desktop.sh')
(root/'workspace/crash').unlink(missing_ok=True)
def request(method):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(45);s.connect(str(root/'helper.sock'));s.sendall((json.dumps({'id':uuid.uuid4().hex,'method':method})+'\n').encode());return json.loads(s.makefile().readline())
def start_helper():
 p=subprocess.Popen([str(exe),str(root),str(res)],stdout=(root/'helper.log').open('ab'),stderr=subprocess.STDOUT)
 for _ in range(100):
  try:request('status');return p
  except (OSError,ValueError):time.sleep(.1)
 raise RuntimeError('helper not ready')
def wait_phase(phase):
 for _ in range(300):
  state=request('status')['result']
  if state['phase']==phase:return state
  if state['phase']=='failed' and phase!='failed':raise RuntimeError(state)
  time.sleep(.2)
 raise RuntimeError('timeout '+phase)
p=start_helper()
try:
 a=request('start');b=request('start');result['duplicate_start_same_generation']=a['result']['generation']==b['result']['generation']
 result['cancel_during_start']=request('stop')['result']['phase']=='stopped'
 (root/'workspace/crash').unlink(missing_ok=True)
 request('start');wait_phase('running')
 for _ in range(100):
  if (root/'workspace/proof').exists():break
  time.sleep(.1)
 assert (root/'workspace/proof').read_text().strip()=='guest-ready'
 result['real_guest_started']=True
 (root/'workspace/crash').touch();result['guest_exit']=wait_phase('failed')
 (root/'workspace/crash').unlink();request('start');wait_phase('running')
 p.kill();p.wait(timeout=10);result['helper_sigkill_exit']=p.returncode
 p=start_helper();result['stale_socket_recovered']=request('status')['result']['phase']=='stopped'
 request('start');wait_phase('running');result['restart_after_crash']=request('stop')['result']['phase']=='stopped'
 request('shutdown');p.wait(timeout=10)
 # Missing generated initfs is rebuilt from the pinned cached OCI init image.
 initfs=root/'store/initfs.ext4';backup=initfs.with_suffix('.backup')
 initfs.rename(backup)
 try:
  p=start_helper();result['missing_initfs_rebuilt']='result' in request('prepare') and initfs.exists();request('shutdown');p.wait(timeout=10)
 finally:backup.unlink(missing_ok=True)
 # Missing kernel fails explicitly, then restoration can recover.
 kernel=res/'vmlinux';kernel.rename(res/'vmlinux.disabled')
 try:
  p=start_helper();result['missing_kernel']=request('prepare');request('shutdown');p.wait(timeout=10)
 finally:(res/'vmlinux.disabled').rename(kernel)
finally:
 if p.poll() is None:p.terminate();p.wait(timeout=20)
pathlib.Path('poc/native-helper/evidence/lifecycle.json').write_text(json.dumps(result,ensure_ascii=False,indent=2))
assert result['duplicate_start_same_generation'] and result['cancel_during_start'] and result['restart_after_crash']
assert 'error' in result['missing_kernel']
print('Real VM lifecycle fault checks passed')
