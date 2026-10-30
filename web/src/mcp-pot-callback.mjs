import {scopedPath} from './adapters/production.mjs';
import {translate} from './locales/messages.mjs';
import {storedPreference} from './state/preferences.mjs';
import {ensureBrowserAccess} from './adapters/access.mjs';
import {getBusinessSocket} from './adapters/ws-client.mjs';
const root=document.querySelector('#connection');
const locale=()=>storedPreference('pot-ui-locale',navigator.language.startsWith('zh')?'zh-CN':'en');
const t=key=>translate(locale(),key);
async function complete(){
 const params=new URLSearchParams(location.search),state=params.get('state'),code=params.get('code');
 // Consume sensitive query data before rendering or making another request.
 history.replaceState(null,'',location.pathname);
 await ensureBrowserAccess(root);
 if(!state||!code||params.has('error'))throw Error('authorization_rejected');
 await getBusinessSocket().request(scopedPath('/api/v1/agent-links/mcp',{space:params.get('space_id')||''}),{method:'POST',body:{action:'complete',state,code}});
 root.textContent=t('potMCP.complete');const link=document.createElement('a');link.href='/';link.className='control';link.textContent=t('potMCP.return');root.append(link);
}
complete().catch(()=>{root.textContent=t('potMCP.failed');});
