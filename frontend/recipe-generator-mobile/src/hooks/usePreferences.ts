import { useEffect, useState } from 'react';
import apiService from '../services/apiService';
import { UserPreferences } from '../types';

/**
 * The user's preferences, served from `apiService.cachedPreferences` on the first frame and
 * refreshed in the background.
 *
 * Every planner screen needs `week_start_day` *before* it renders — starting from `null` and
 * falling back to Monday meant the week view drew a Monday-first week on open and then visibly
 * re-laid-out to the saved Sunday start once the fetch landed. The cache is a module-level
 * value on the service, so only the first mount of an app session pays for it.
 *
 * Still refetched on every mount: preferences are edited elsewhere in the app (and the cache
 * is not shared across devices), so the cached value is a starting frame, not the truth.
 */
export function usePreferences(): UserPreferences | null {
  const [prefs, setPrefs] = useState<UserPreferences | null>(apiService.cachedPreferences);

  useEffect(() => {
    apiService.getPreferences().then(setPrefs)
      .catch(error => console.error('Error loading preferences:', error));
  }, []);

  return prefs;
}
