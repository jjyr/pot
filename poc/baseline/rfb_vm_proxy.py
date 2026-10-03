"""Loopback-only test proxy to a temporary Linux VM VNC server. No production auth."""
import base64,hashlib,json,os,socket,struct,threading,time
import rfb_fixture as fixture
from http.server import ThreadingHTTPServer
from pathlib import Path
ROOT=fixture.ROOT
fixture.PAGE=fixture.PAGE.replace('合成画面（非 Linux 桌面）','真实 Linux VM / headed Chromium')
fixture.PAGE=fixture.PAGE.replace("rfb.viewOnly=false;rfb.sendKey","rfb.viewOnly=false;rfb.sendKey(0xffe3,'ControlLeft',true);rfb.sendKey(0x6c,'KeyL',true);rfb.sendKey(0x6c,'KeyL',false);rfb.sendKey(0xffe3,'ControlLeft',false);rfb.sendKey")
class Handler(fixture.Handler):
 def rfb(self):
  upstream=socket.create_connection((os.environ['POT_VNC_HOST'],5900),timeout=10)
  key=self.headers['Sec-WebSocket-Key'];accept=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
  self.send_response(101);self.send_header('Upgrade','websocket');self.send_header('Connection','Upgrade');self.send_header('Sec-WebSocket-Accept',accept)
  if self.headers.get('Sec-WebSocket-Protocol'):self.send_header('Sec-WebSocket-Protocol','binary')
  self.end_headers();self.wfile.flush()
  events=[]
  def exact(n):
   b=self.rfile.read(n)
   if len(b)!=n:raise EOFError()
   return b
  def stream():
   try:
    while True:
     b=upstream.recv(65536)
     if not b:break
     n=len(b);head=bytes([0x82,n]) if n<126 else bytes([0x82,126])+struct.pack('>H',n) if n<65536 else bytes([0x82,127])+struct.pack('>Q',n)
     self.connection.sendall(head+b)
   except OSError:pass
  threading.Thread(target=stream,daemon=True).start()
  try:
   while True:
    a,b=exact(2);n=b&127
    if n==126:n=struct.unpack('>H',exact(2))[0]
    elif n==127:n=struct.unpack('>Q',exact(8))[0]
    mask=exact(4) if b&128 else None;p=bytearray(exact(n))
    if mask:
     for i in range(n):p[i]^=mask[i%4]
    if a&15==8:break
    if a&15 in (0,1,2):
     upstream.sendall(p)
     events.append({'bytes':len(p),'rfb_key_message':len(p)==8 and p[0]==4})
     (ROOT/'rfb-vm-events.json').write_text(json.dumps(events,indent=2))
  except (EOFError,OSError):pass
  finally:upstream.close()
if __name__=='__main__':
 os.chdir(ROOT);ThreadingHTTPServer(('127.0.0.1',17410),Handler).serve_forever()
