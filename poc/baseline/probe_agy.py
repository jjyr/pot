"""Antigravity SDK lifecycle probe. Uses a non-working fixture key; NEVER sends chat/model request."""
import asyncio,json,inspect,importlib.metadata
from pathlib import Path
from google.antigravity import Agent, LocalAgentConfig
from google.antigravity.types import SessionContinuationMode
from google.antigravity.hooks import policy
ROOT=Path('/tmp/pot-research-20261003')
async def main():
 result={'version':importlib.metadata.version('google-antigravity'),'no_model_calls':True}
 config=LocalAgentConfig(api_key='poc-placeholder-not-a-real-key',workspaces=[str(ROOT/'agy-workspace')],app_data_dir=str(ROOT/'homes/agy-sdk'),save_dir=str(ROOT/'homes/agy-sessions'),conversation_id='pot-poc-000000000000000000000000001',session_continuation_mode=SessionContinuationMode.CREATE_OR_RESUME,policies=[policy.deny_all()],env={'GEMINI_API_KEY':'poc-placeholder-not-a-real-key'})
 try:
  async with asyncio.timeout(30):
   async with Agent(config) as agent:
    result['started']=agent.is_started;result['conversation_id']=agent.conversation_id
    result['conversation_methods']=[x for x in dir(agent.conversation) if not x.startswith('_')]
    await agent.conversation.connection.cancel();result['cancel_rpc']=True
  result['clean_shutdown']=True
 except Exception as e:result['error']=type(e).__name__+': '+str(e)[:800]
 Path(__file__).parent.joinpath('evidence/agy-sdk.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,indent=2))
if __name__=='__main__':asyncio.run(main())
