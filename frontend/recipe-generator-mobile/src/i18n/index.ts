import { I18n } from 'i18n-js';
import { getLocales } from 'expo-localization';
import en from './en';
import de from './de';

// The UI catalogs. The *content* half of i18n was already handled — apiService sends the
// locale on every /chat turn and the model answers in it — so this only covers chrome.
export const i18n = new I18n({ en, de });
i18n.defaultLocale = 'en';
i18n.enableFallback = true;

export type Language = 'en' | 'de';

export const isLanguage = (v: unknown): v is Language => v === 'en' || v === 'de';

// What 'system' resolves to. Anything that is not German is English — there is no third catalog.
export const deviceLanguage = (): Language =>
  getLocales()[0]?.languageCode === 'de' ? 'de' : 'en';

// Callers that are not React components (pure date formatters, the API service) read the
// singleton at call time. Components must use `useLanguage().t` instead, or they will not
// re-render when the language changes.
export const currentLocale = (): Language => (isLanguage(i18n.locale) ? i18n.locale : 'en');
