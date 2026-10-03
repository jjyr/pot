"""Real remote broker acceptance. Requires P2 + running broker; no fixture."""
import asyncio,json,pathlib,struct
from aiohttp import ClientSession
async def main():
 root=pathlib.Path('.local/poc/integrated');token=(root/'url').read_text().split('#')[1];base='http://127.0.0.1:17681';out={}
 async with ClientSession() as s:
  async def api(path,body=None):
   async with s.request('POST' if body else 'GET',base+'/api/'+path,headers={'X-Pot-Token':token},json=body) as r:return r.status,await r.text()
  async with s.get(base+'/api/status') as r:out['unauthenticated_rejected']=r.status==403
  await api('control',{'action':'return'})
  async with s.ws_connect(base+'/rfb?token='+token) as ws:
   buf=bytearray()
   async def read(n):
    while len(buf)<n:buf.extend((await ws.receive()).data)
    v=bytes(buf[:n]);del buf[:n];return v
   assert (await read(12)).startswith(b'RFB');await ws.send_bytes(b'RFB 003.008\n')
   count=(await read(1))[0];types=await read(count);assert 1 in types
   await ws.send_bytes(b'\x01');assert await read(4)==b'\0'*4;await ws.send_bytes(b'\x01')
   init=await read(24);await read(int.from_bytes(init[20:24],'big'))
   await ws.send_bytes(struct.pack('>BBHi',2,0,1,-312))
   await ws.send_bytes(struct.pack('>BBHI',4,1,0,ord('z'))+struct.pack('>BBHI',4,0,0,ord('z')))
   write=asyncio.create_task(api('driver',{'text':'broker concurrency 中文'}));await asyncio.sleep(.02)
   take=asyncio.create_task(api('control',{'action':'take'}));wr,tr=await asyncio.gather(write,take)
   out['inflight_driver_finished_before_take']=wr[0]==tr[0]==200
   out['human_rejects_driver']=(await api('driver',{'text':'must not arrive'}))[0]==409
   # Actual QEMU modifier held, then return must issue the matching release.
   await ws.send_bytes(struct.pack('>BBHII',255,0,1,0xffe3,29));await asyncio.sleep(.05)
   out['return_observed']=json.loads((await api('control',{'action':'return'}))[1])['observed']
  rows=[json.loads(x) for x in (root/'events.jsonl').read_text().splitlines()]
  out['raw_rfb_input_rejected']=any(r['event']=='viewer.input.rejected' and r.get('kind')==4 for r in rows)
  assert all(out[k] for k in ['unauthenticated_rejected','inflight_driver_finished_before_take','human_rejects_driver','raw_rfb_input_rejected'])
 pathlib.Path('poc/integrated-desktop/evidence/broker-acceptance.json').write_text(json.dumps(out,ensure_ascii=False,indent=2))
 print('Real broker acceptance passed')
asyncio.run(main())
