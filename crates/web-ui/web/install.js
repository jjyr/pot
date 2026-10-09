// Installation never grants access or requests notification permission.
let deferredPrompt, installationAccepted = false, changed = () => {};
const standalone = matchMedia('(display-mode: standalone)');
export const isInstalled = () => standalone.matches || navigator.standalone === true || installationAccepted;
addEventListener('beforeinstallprompt', event => {
  event.preventDefault(); deferredPrompt = event; changed();
});
addEventListener('appinstalled', () => {
  installationAccepted = true; deferredPrompt = null; changed();
});
standalone.addEventListener('change', () => changed());
export function createInstaller(t, onChange) {
  changed = onChange;
  // Register independently of login and VAPID configuration. No offline cache.
  let registration;
  const worker = 'serviceWorker' in navigator && isSecureContext
    ? navigator.serviceWorker.register('/sw.js', {scope: '/'}).then(r => { registration = r; return r; }).catch(() => null)
    : Promise.resolve(null);
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  function help(key) {
    document.getElementById('install-help')?.remove();
    const dialog = document.createElement('dialog'); dialog.id = 'install-help';
    const title = document.createElement('h2'); title.textContent = t('mobile.install');
    const text = document.createElement('p'); text.textContent = t(key);
    const close = document.createElement('button'); close.textContent = t('mobile.install-close');
    close.onclick = () => dialog.close();
    dialog.append(title, text, close); document.body.append(dialog);
    dialog.addEventListener('close', () => dialog.remove()); dialog.showModal();
  }
  return {
    async registration() {
      const r = registration || await worker;
      if (!r) throw Error(t('mobile.install-https'));
      return navigator.serviceWorker.ready;
    },
    async open() {
      if (isInstalled()) { help('mobile.installed-hint'); return; }
      if (!isSecureContext) { help('mobile.install-https'); return; }
      if (deferredPrompt) {
        const prompt = deferredPrompt; deferredPrompt = null;
        try { await prompt.prompt(); await prompt.userChoice; }
        catch { help('mobile.install-browser'); }
        // Acceptance is not proof of installation. Wait for appinstalled.
        changed(); return;
      }
      help(ios ? 'mobile.install-ios' : 'mobile.install-browser');
    }
  };
}
