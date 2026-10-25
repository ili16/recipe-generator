import { useEffect, useState } from 'react';
import authService from '../services/authService';

/**
 * Whether there is a session, as `null` until the check resolves — screens gate on it so a
 * signed-out visitor gets the sign-in prompt rather than whatever their first 401 looks like.
 */
export function useIsAuthenticated(): boolean | null {
  const [authed, setAuthed] = useState<boolean | null>(null);
  useEffect(() => {
    authService.isAuthenticated().then(setAuthed).catch(() => setAuthed(false));
  }, []);
  return authed;
}
