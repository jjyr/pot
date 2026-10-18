import {mountApp} from './components/ui.js';
import {createBrowserClient} from './adapters/browser.js';
import {ensureBrowserAccess, installPWA, retireBrowserPush} from './adapters/access.mjs';
import {APIError, createProductionClient} from './adapters/production.mjs';

const root = document.querySelector('#app');
const params = new URLSearchParams(location.search);
const desktop = params.get('platform') === 'desktop';
document.documentElement.dataset.platform = desktop ? 'desktop' : 'browser';
try {
  let client;
  const access = desktop ? null : await ensureBrowserAccess(root);
  // A fixture may mount only when explicitly selected by the host or confirmed
  // by the isolated preview identity after a production endpoint HTTP 404.
  if (desktop && params.get('mode') === 'preview') client = (await import('./adapters/desktop.js')).createDesktopClient();
  else {
    try { client = await createProductionClient({desktop}); }
    catch (error) {
      if (desktop || !(error instanceof APIError) || error.status !== 404) throw error;
      const preview = createBrowserClient();
      const snapshot = await preview.snapshot();
      if (snapshot.runtime_id !== 'go-ui-preview') throw error;
      client = preview;
    }
  }
  if(client.production&&!desktop){client.access=access;installPWA().catch(()=>{});retireBrowserPush();}
  document.title = client.production ? 'Pot' : 'Pot — Go developer preview';
  const dispose = mountApp(root, client, {surface: params.get('surface') === 'tray' ? 'tray' : 'main'});
  window.addEventListener('pagehide', event => { if (!event.persisted) dispose(); });
} catch (error) {
  root.className = 'startup-error';
  root.textContent = `Pot could not start: ${error.message}`;
  const retry = document.createElement('button'); retry.textContent = 'Retry'; retry.onclick = () => location.reload(); root.append(retry);
}
