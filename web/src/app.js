import {mountApp} from './components/ui.js';
import {createBrowserClient} from './adapters/browser.js';

const root = document.querySelector('#app');
const params = new URLSearchParams(location.search);
document.documentElement.dataset.platform = params.get('platform') === 'desktop' ? 'desktop' : 'browser';
try {
  const client = params.get('platform') === 'desktop'
    ? (await import('./adapters/desktop.js')).createDesktopClient()
    : createBrowserClient();
  const dispose = mountApp(root, client, {surface: params.get('surface') === 'tray' ? 'tray' : 'main'});
  window.addEventListener('pagehide', event => { if (!event.persisted) dispose(); });
} catch (error) {
  root.textContent = `Go developer preview could not start: ${error.message}`;
}
