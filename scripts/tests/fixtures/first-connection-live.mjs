// The opt-in fixture supplies a disposable browser login and clipboard invitation.
// Product HTTP, authentication, preview, pairing and management are unmocked.
import {mountApp} from '../../../web/src/components/ui.js';
import {createProductionClient} from '../../../web/src/adapters/production.mjs';
await fetch('/fixture/login',{method:'POST'});
Object.defineProperty(navigator,'clipboard',{configurable:true,value:{readText:async()=>{const result=await fetch('/fixture/invitation',{method:'POST'});if(!result.ok)throw Error('Fixture invitation unavailable');return(await result.json()).address;}}});
const client=await createProductionClient();
window.firstConnectionLive={client,dispose:mountApp(document.querySelector('#app'),client)};
