import { useEffect, useState } from 'react';
import { Platform } from 'react-native';
import { StatusBar } from 'expo-status-bar';
import { useFonts } from 'expo-font';
import { fontAssets } from './src/theme';
import AppShell from './src/navigation/AppShell';
import { ThemeProvider, useTheme } from './src/context/ThemeContext';
import { AlertProvider } from './src/context/AlertContext';
import { LanguageProvider, useLanguage } from './src/context/LanguageContext';
import authService from './src/services/authService';
import Loading from './src/components/Loading';
import SharedRecipeScreen from './src/screens/SharedRecipeScreen';
import SharedPlanScreen from './src/screens/SharedPlanScreen';

// Web only: /s/<token> is a public, read-only recipe (BACKLOG 8.4). Read straight off
// the URL rather than through the navigator — the shared view has no nav, no auth and
// nothing else in the app links to it.
const sharedToken =
  Platform.OS === 'web' ? window.location.pathname.match(/^\/s\/([\w-]+)$/)?.[1] : undefined;

// Same deal for /p/<token>: a public, read-only week and its shopping list (BACKLOG 13.2).
const sharedPlanToken =
  Platform.OS === 'web' ? window.location.pathname.match(/^\/p\/([\w-]+)$/)?.[1] : undefined;

// Web only: if we just landed back from Keycloak's redirect (see
// authService.login), there's a `?code=&state=` pair to exchange for tokens
// before anything that checks auth status renders.
const hasPendingWebOAuthCallback =
  Platform.OS === 'web' && new URLSearchParams(window.location.search).has('code');

function Root() {
  const { isDark } = useTheme();
  const { t, hydrateFromAccount } = useLanguage();
  const [authReady, setAuthReady] = useState(!hasPendingWebOAuthCallback);
  // Gate the first render on the brand fonts so nothing flashes in the system face and reflows
  // (BACKLOG 5.6). `error` counts as ready: a failed font download degrades to system fonts, it
  // does not hold the app on a spinner forever.
  const [fontsLoaded, fontError] = useFonts(fontAssets);

  useEffect(() => {
    if (!authReady) {
      authService.completeWebLoginIfNeeded().finally(() => setAuthReady(true));
      return;
    }
    // The account's language, once there is a session to ask (BACKLOG 14.1). Here rather than
    // in the provider because this is the only place that knows a pending web login has
    // finished — and a provider that fetched on mount would ask mid-redirect and get a 401.
    hydrateFromAccount();
  }, [authReady, hydrateFromAccount]);

  if (!fontsLoaded && !fontError) {
    return <Loading visible />;
  }

  if (sharedToken) {
    return (
      <>
        <SharedRecipeScreen token={sharedToken} />
        <StatusBar style={isDark ? 'light' : 'dark'} />
      </>
    );
  }

  if (sharedPlanToken) {
    return (
      <>
        <SharedPlanScreen token={sharedPlanToken} />
        <StatusBar style={isDark ? 'light' : 'dark'} />
      </>
    );
  }

  if (!authReady) {
    return <Loading visible message={t('auth.signingIn')} />;
  }

  return (
    <>
      <AppShell />
      <StatusBar style={isDark ? 'light' : 'dark'} />
    </>
  );
}

export default function App() {
  return (
    <LanguageProvider>
      <ThemeProvider>
        <AlertProvider>
          <Root />
        </AlertProvider>
      </ThemeProvider>
    </LanguageProvider>
  );
}
