import AsyncStorage from '@react-native-async-storage/async-storage';

/**
 * Whether the Staples section is folded away (BACKLOG 12.6). A view preference, not an
 * account fact, so it lives here rather than in `user_preferences` — same shape as
 * `feedbackPromptStore.ts`: try/catch reads, fire-and-forget writes.
 *
 * Deliberately not in `STORAGE_KEYS`: `authService.clearSession()` wipes those on logout, and
 * signing out is not a request to unfold the salt again.
 */
const STAPLES_COLLAPSED_KEY = 'pantry_staples_collapsed_v1';

/** Collapsed by default: a staple is by definition the thing you never have to look at. */
export async function readStaplesCollapsed(): Promise<boolean> {
  try {
    const raw = await AsyncStorage.getItem(STAPLES_COLLAPSED_KEY);
    return raw === null ? true : raw === '1';
  } catch {
    return true;
  }
}

export function writeStaplesCollapsed(collapsed: boolean): void {
  AsyncStorage.setItem(STAPLES_COLLAPSED_KEY, collapsed ? '1' : '0').catch(() => {});
}
