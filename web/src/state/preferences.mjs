// Keep 0.0.1 preferences readable without overwriting either generation.
// Reading remains useful in storage-restricted WebViews where copying fails.
export function storedPreference(key, fallback, storage) {
  try {
    storage ??= globalThis.localStorage;
    const current = storage.getItem(key);
    if (current !== null) return current || fallback;
    const legacy = {'pot-ui-locale':'pot-go-preview-locale','pot-ui-theme':'pot-go-preview-theme'}[key];
    const value = legacy ? storage.getItem(legacy) : null;
    if (value !== null) {
      try { storage.setItem(key, value); } catch { /* restricted WebView */ }
      return value || fallback;
    }
  } catch { /* restricted WebView */ }
  return fallback;
}
