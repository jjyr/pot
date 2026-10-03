"""Local RFB 3.8/WebSocket fixture for a real noVNC client, NOT a Linux desktop."""
import base64,hashlib,json,socket,struct,threading,time
from http.server import ThreadingHTTPServer,SimpleHTTPRequestHandler
from pathlib import Path
ROOT=Path('/tmp/pot-research-20261003');events=[]
PAGE='''<!doctype html><meta charset="utf-8"><style>html,body{margin:0;background:#101b25;color:#ddd;font:16px system-ui}header{padding:16px}#screen{height:500px;width:100%}</style><header>真实 noVNC / RFB 传输测试 · 合成画面（非 Linux 桌面）<br><span id="state">Connecting</span></header><div id="screen"></div><script type="module">
import RFB from '/novnc/core/rfb.js';
window.pocState={connected:false};
const rfb = new RFB(document.getElementById('screen'),'ws://127.0.0.1:17410/rfb');
rfb.viewOnly=true;rfb.scaleViewport=true;
rfb.addEventListener('connect',()=>{window.pocState={connected:true,viewOnly:rfb.viewOnly};document.getElementById('state').textContent='RFB 已连接 · 正在查看'});
window.pocSendKey=()=>{rfb.viewOnly=false;rfb.sendKey(0x61,'KeyA',true);rfb.sendKey(0x61,'KeyA',false);window.pocState.sentKey=true;};
</script>'''
class Handler(SimpleHTTPRequestHandler):
 def log_message(self,*args):pass
 def do_GET(self):
  if self.path=='/':
   b=PAGE.encode();self.send_response(200);self.send_header('Content-Type','text/html; charset=utf-8');self.send_header('Content-Length',str(len(b)));self.end_headers();self.wfile.write(b);return
  if self.path.startswith('/novnc/'):
   self.path='/noVNC/'+self.path[len('/novnc/'):];return super().do_GET()
  if self.path=='/rfb':return self.rfb()
  return super().do_GET()
 def rfb(self):
  key=self.headers.get('Sec-WebSocket-Key','');accept=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
  self.send_response(101);self.send_header('Upgrade','websocket');self.send_header('Connection','Upgrade');self.send_header('Sec-WebSocket-Accept',accept)
  if self.headers.get('Sec-WebSocket-Protocol'):self.send_header('Sec-WebSocket-Protocol','binary')
  self.end_headers();self.wfile.flush();self.connection.settimeout(30)
  def send(b):
   n=len(b);head=bytes([0x82,n]) if n<126 else bytes([0x82,126])+struct.pack('>H',n) if n<65536 else bytes([0x82,127])+struct.pack('>Q',n)
   self.wfile.write(head+b);self.wfile.flush()
  def exact(n):
   b=self.rfile.read(n)
   if len(b)!=n:raise EOFError()
   return b
  buffer=bytearray()
  def read(n):
   while len(buffer)<n:
    a,b=exact(2);length=b&127
    if length==126:length=struct.unpack('>H',exact(2))[0]
    elif length==127:length=struct.unpack('>Q',exact(8))[0]
    mask=exact(4) if b&128 else None;payload=bytearray(exact(length))
    if mask:
     for i in range(length):payload[i]^=mask[i%4]
    if a&15==8:raise EOFError()
    if a&15 in (0,1,2):buffer.extend(payload)
   x=bytes(buffer[:n]);del buffer[:n];return x
  try:
   send(b'RFB 003.008\n');read(12);send(b'\x01\x01');read(1);send(b'\0\0\0\0');read(1)
   w,h=320,240;name=b'Pot RFB fixture'
   pixel=struct.pack('>BBBBHHHBBB3x',32,24,0,1,255,255,255,16,8,0)
   send(struct.pack('>HH',w,h)+pixel+struct.pack('>I',len(name))+name)
   events.append({'event':'rfb_connected','time':time.time()});(ROOT/'rfb-events.json').write_text(json.dumps(events,indent=2))
   while True:
    typ=read(1)[0]
    if typ==0:read(19)
    elif typ==2:
     head=read(3);count=struct.unpack('>H',head[1:])[0];read(count*4)
    elif typ==3:
     read(9);pixels=bytearray()
     for y in range(h):
      for x in range(w):pixels.extend(bytes([110 if x<w//2 else 160,110 if y<h//2 else 190,50,0]))
     send(struct.pack('>BBH',0,0,1)+struct.pack('>HHHHi',0,0,w,h,0)+pixels)
    elif typ==4:
     b=read(7);events.append({'event':'key','down':b[0],'keysym':struct.unpack('>I',b[3:])[0]});(ROOT/'rfb-events.json').write_text(json.dumps(events,indent=2))
    elif typ==5:read(5)
    elif typ==6:
     b=read(7);read(struct.unpack('>I',b[3:])[0])
    else:raise RuntimeError('unsupported fixture message '+str(typ))
  except (EOFError,socket.timeout,BrokenPipeError,ConnectionResetError):pass
if __name__=='__main__':
 import os
 os.chdir(ROOT);ThreadingHTTPServer(('127.0.0.1',17410),Handler).serve_forever()
