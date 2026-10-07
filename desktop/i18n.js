// Shared desktop localization. Resources are generated from the page's translation keys.
(() => {
    let catalogs = {}, requestedLocale;
    const normalize = value => value.replaceAll('_', '-').toLowerCase();
    function match(language) {
        const normalized = normalize(language);
        const available = Object.keys(catalogs);
        return available.find(locale => normalize(locale) === normalized)
            ?? available.find(locale => normalize(locale).split('-')[0] === normalized.split('-')[0]);
    }
    window.potLocale = 'en';
    window.potT = key => catalogs[window.potLocale]?.[key] ?? catalogs.en?.[key] ?? key;
    window.potF = (key, args) => potT(key).replace(/\{(\w+)\}/g, (token, name) => args[name] ?? token);
    window.potNumber = (number, digits = 0) => new Intl.NumberFormat(window.potLocale, {minimumFractionDigits: digits, maximumFractionDigits: digits}).format(number);
    window.potSetLocale = language => {
        requestedLocale = language;
        if (!catalogs.en) return;
        window.potLocale = match(language) ?? 'en';
        document.documentElement.lang = window.potLocale;
        try { localStorage.setItem('pot-language', window.potLocale); } catch {}
        document.querySelectorAll('[data-pot-i18n]').forEach(el => el.textContent = potT(el.dataset.potI18n));
        for (const attribute of ['title', 'aria-label']) {
            document.querySelectorAll('[data-pot-' + attribute + ']').forEach(el => el.setAttribute(attribute, potT(el.getAttribute('data-pot-' + attribute))));
        }
        window.dispatchEvent(new Event('pot-locale'));
    };
    window.potLocalizationReady = (async () => {
        const response = await fetch(new URL('./locales.json', document.currentScript.src), {cache: 'no-store'});
        if (!response.ok) throw Error('Desktop translations unavailable');
        catalogs = await response.json();
        if (document.readyState === 'loading') await new Promise(resolve => document.addEventListener('DOMContentLoaded', resolve, {once: true}));
        let saved;
        try { saved = localStorage.getItem('pot-language'); } catch {}
        const initial = requestedLocale ?? (saved && match(saved)) ?? (navigator.languages ?? []).map(match).find(Boolean) ?? 'en';
        potSetLocale(initial);
    })();
})();
