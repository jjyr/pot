#!/usr/bin/env python3
import json,socket,sys,uuid
with socket.socket(socket.AF_UNIX) as s:
 s.settimeout(120);s.connect(sys.argv[1]);s.sendall((json.dumps({'id':uuid.uuid4().hex,'method':sys.argv[2]})+'\n').encode())
 print(s.makefile().readline().strip())
