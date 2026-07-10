// Engine i18n minimale. Carica i dizionari, persiste la lingua scelta in cookie
// (no localStorage per coerenza con la gestione sessione lato server) e offre
// la funzione t(key, params) con fallback alla chiave.
import it from "../i18n/it.js";
import en from "../i18n/en.js";

const dictionaries = { it, en };
let current = detectLocale();

function detectLocale() {
  const m = document.cookie.match(/(?:^|;\s*)nasd_locale=([a-z]{2})/);
  if (m && dictionaries[m[1]]) return m[1];
  const nav = (navigator.language || "it").slice(0, 2);
  return dictionaries[nav] ? nav : "it";
}

export function getLocale() {
  return current;
}

export function setLocale(locale) {
  if (!dictionaries[locale]) return;
  current = locale;
  document.cookie = `nasd_locale=${locale}; path=/; max-age=31536000; samesite=strict`;
  document.documentElement.lang = locale;
  window.dispatchEvent(new CustomEvent("locale-changed", { detail: locale }));
}

// t risolve una chiave puntata ("raid.states.degraded") e interpola {param}.
export function t(key, params = {}) {
  const parts = key.split(".");
  let node = dictionaries[current];
  for (const p of parts) {
    node = node?.[p];
    if (node === undefined) return key; // fallback visibile per chiavi mancanti
  }
  if (typeof node !== "string") return key;
  return node.replace(/\{(\w+)\}/g, (_, k) => (k in params ? params[k] : `{${k}}`));
}
