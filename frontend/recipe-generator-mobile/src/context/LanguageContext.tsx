import React, { createContext, useCallback, useContext, useEffect, useRef, useState, ReactNode } from 'react';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { STORAGE_KEYS } from '../constants';
import { i18n, deviceLanguage, isLanguage, Language } from '../i18n';
import apiService from '../services/apiService';

// Same shape as ThemeContext, for the same reason: an OS default the user can override. Unlike
// the theme, the choice is also an account setting (BACKLOG 14.1): the device copy is what the
// first frame and a signed-out visitor get, and `users.language` is what a second device reads.
// The UI language also decides the `language` the backend gets on a chat turn (see apiService),
// so switching here switches what the model writes back.
export type LanguageMode = 'system' | Language;

const isMode = (v: unknown): v is LanguageMode => v === 'system' || isLanguage(v);

interface LanguageContextValue {
  locale: Language;
  mode: LanguageMode;
  setMode: (mode: LanguageMode) => void;
  /** Adopt the signed-in account's language. No-op when signed out. */
  hydrateFromAccount: () => void;
  t: (key: string, options?: Record<string, unknown>) => string;
}

const LanguageContext = createContext<LanguageContextValue>({
  locale: 'en',
  mode: 'system',
  setMode: () => {},
  hydrateFromAccount: () => {},
  t: (key) => key,
});

export const LanguageProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  // 'system' is also the pre-hydration value, so the first frame is already in the OS language.
  const [mode, setModeState] = useState<LanguageMode>('system');
  // The account outranks the device copy, and the two are read by racing promises — without
  // this the slower AsyncStorage read can land last and put the stale device value back.
  const accountAnswered = useRef(false);

  useEffect(() => {
    AsyncStorage.getItem(STORAGE_KEYS.LANGUAGE)
      .then((stored) => { if (isMode(stored) && !accountAnswered.current) setModeState(stored); })
      .catch(() => {}); // a missing preference just means "system"
  }, []);

  // Called once auth has settled, not on mount: on web the app can still be exchanging an
  // OAuth code when this provider mounts, and a fetch then is a guaranteed 401.
  const hydrateFromAccount = useCallback(() => {
    apiService.getPreferences()
      .then((prefs) => {
        if (!isLanguage(prefs.language)) return;
        accountAnswered.current = true;
        setModeState(prefs.language);
        // Also to the device, so the next cold start is already in it rather than opening in
        // the device locale and switching once this fetch lands.
        AsyncStorage.setItem(STORAGE_KEYS.LANGUAGE, prefs.language).catch(() => {});
      })
      .catch(() => {}); // signed out, or offline: the device value stands
  }, []);

  const setMode = (next: LanguageMode) => {
    setModeState(next);
    AsyncStorage.setItem(STORAGE_KEYS.LANGUAGE, next).catch(() => {});
    // PATCH /preferences overwrites the whole object, so the write has to carry the rest of
    // them. `cachedPreferences` is non-null exactly when a fetch has succeeded — i.e. when
    // there is an account to write to; signed out, the device copy is the only copy.
    const prefs = apiService.cachedPreferences;
    if (prefs) {
      apiService.updatePreferences({ ...prefs, language: next === 'system' ? null : next })
        .catch(() => {}); // the device already has it; a failed sync is not worth a dialog
    }
  };

  const locale: Language = mode === 'system' ? deviceLanguage() : mode;
  // Set during render, not in an effect: non-component callers (date formatters, apiService)
  // read `i18n.locale` at call time and must never see the previous language.
  i18n.locale = locale;

  // Identity changes with the locale — that is what re-renders every consumer on a switch.
  const t = useCallback((key: string, options?: Record<string, unknown>) => i18n.t(key, options), [locale]);

  return (
    <LanguageContext.Provider value={{ locale, mode, setMode, hydrateFromAccount, t }}>
      {children}
    </LanguageContext.Provider>
  );
};

export const useLanguage = () => useContext(LanguageContext);
