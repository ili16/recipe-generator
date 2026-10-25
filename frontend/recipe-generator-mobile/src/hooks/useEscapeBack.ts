import { useEffect } from 'react';
import { Platform } from 'react-native';
import { useNavigation } from '@react-navigation/native';

/**
 * On web, listens for the Escape key and calls navigation.goBack().
 * No-op on native (the system back gesture/button handles it there).
 */
export function useEscapeBack() {
  const navigation = useNavigation();

  useEffect(() => {
    if (Platform.OS !== 'web') return;

    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && navigation.canGoBack()) {
        navigation.goBack();
      }
    };

    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [navigation]);
}
