"""Ask Chromium to close through CDP, then let the supervisor wait for exit."""
import asyncio
import aiohttp

async def main():
    async with aiohttp.ClientSession() as session:
        async with session.get('http://127.0.0.1:9222/json/version') as response:
            info = await response.json()
        async with session.ws_connect(info['webSocketDebuggerUrl']) as ws:
            await ws.send_json({'id': 1, 'method': 'Browser.close'})
            try:
                await asyncio.wait_for(ws.receive(), 3)
            except (asyncio.TimeoutError, aiohttp.ClientError):
                pass

asyncio.run(main())
