// Runs synchronously before styles or application code, including on reload.
(() => {
  const read = key => { try { return localStorage.getItem(key); } catch { return null; } };
  const write = (key, value) => { try { localStorage.setItem(key, value); } catch {} };
  const system = matchMedia('(prefers-color-scheme: dark)');
  let appearance = Number(read('pot-appearance') || 0);
  if (![0, 1, 2].includes(appearance)) appearance = 0;
  const preferences = window.potPreferences = {
    appearance,
    locale: read('pot-locale') || navigator.languages?.[0] || navigator.language || 'en',
    get dark() { return this.appearance === 2 || (this.appearance === 0 && system.matches); },
    apply() {
      const root = document.documentElement;
      root.dataset.theme = this.dark ? 'dark' : 'light';
      root.style.colorScheme = root.dataset.theme;
      root.lang = this.locale;
      document.querySelector('meta[name="theme-color"]')?.setAttribute('content', this.dark ? '#202020' : '#fdfdfd');
    },
    setAppearance(value) {
      this.appearance = [0, 1, 2].includes(Number(value)) ? Number(value) : 0;
      write('pot-appearance', String(this.appearance));
      this.apply();
    },
    setLocale(value) {
      this.locale = value;
      write('pot-locale', value);
      this.apply();
    },
  };
  preferences.apply();
  system.addEventListener('change', () => preferences.apply());
})();
