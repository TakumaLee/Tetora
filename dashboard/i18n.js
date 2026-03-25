// i18n — lightweight internationalization module for Tetora dashboard.
// Must be loaded before all other dashboard scripts.
const i18n = (() => {
  const stored = localStorage.getItem('tetora_locale');
  const detected = (() => {
    const lang = (navigator.language || '').toLowerCase();
    if (lang.startsWith('zh')) return 'zh-tw';
    if (lang.startsWith('ja')) return 'ja';
    return 'en';
  })();
  let locale = stored || detected;
  let strings = {};
  let loaded = false;

  return {
    get locale() { return locale; },
    get isLoaded() { return loaded; },

    async load(lang) {
      lang = lang || locale;
      try {
        const r = await fetch(`/api/locales/${lang}`);
        if (!r.ok) throw new Error(r.status);
        strings = await r.json();
      } catch {
        // Fallback to English if requested locale fails.
        if (lang !== 'en') {
          const r = await fetch('/api/locales/en');
          if (r.ok) strings = await r.json();
        }
      }
      locale = lang;
      loaded = true;
      localStorage.setItem('tetora_locale', lang);
    },

    t(key, ...args) {
      let s = strings[key] ?? key;
      args.forEach((v, i) => { s = s.replace(`{${i}}`, v); });
      return s;
    },

    applyDOM() {
      document.querySelectorAll('[data-i18n]').forEach(el => {
        el.textContent = i18n.t(el.dataset.i18n);
      });
      document.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
        el.placeholder = i18n.t(el.dataset.i18nPlaceholder);
      });
      document.querySelectorAll('[data-i18n-title]').forEach(el => {
        el.title = i18n.t(el.dataset.i18nTitle);
      });
      document.querySelectorAll('[data-i18n-html]').forEach(el => {
        el.innerHTML = i18n.t(el.dataset.i18nHtml);
      });
    }
  };
})();

// Change locale and re-apply all translations.
async function changeLanguage(lang) {
  await i18n.load(lang);
  i18n.applyDOM();
}
