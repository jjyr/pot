export function createBrowserClient(base = '') {
  async function request(path, options = {}) {
    const response = await fetch(`${base}/api/preview/${path}`, {
      credentials: 'same-origin', ...options,
      headers: {'Content-Type': 'application/json', ...options.headers},
    });
    if (!response.ok) throw new Error(`Preview service: HTTP ${response.status}`);
    return response.json();
  }
  return {
    snapshot: options => request('snapshot', options),
    send: value => request('messages', {method: 'POST', body: JSON.stringify(value)}),
    cancel: value => request('cancel', {method: 'POST', body: JSON.stringify(value)}),
    subscribe(onSnapshot, onStatus = () => {}) {
      const source = new EventSource(`${base}/api/preview/events`);
      source.addEventListener('snapshot', event => {
        try { onSnapshot(JSON.parse(event.data)); }
        catch { onStatus('offline'); }
      });
      source.onopen = () => onStatus('connected');
      source.onerror = () => onStatus('offline');
      return () => source.close();
    },
  };
}
