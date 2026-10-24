// Storage can be unavailable in restricted WebViews.
export function storedPreference(key, fallback, storage) {
  try {
    storage ??= globalThis.localStorage;
    return storage.getItem(key) || fallback;
  } catch { /* restricted WebView */ }
  return fallback;
}
