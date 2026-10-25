import { useEffect, useState } from 'react';
import { Keyboard, Platform } from 'react-native';

/**
 * True while the on-screen keyboard covers part of the viewport. The shell uses it to drop the
 * bottom tabs (and the composer's hint line) out of the way while typing — on a phone the
 * keyboard already takes half the screen.
 *
 * Web has no `Keyboard` events, so it reads the visual viewport instead: iOS Safari shrinks it
 * by the keyboard's height, which `public/index.html` already listens to for `--app-height`.
 * ponytail: 150px is a threshold, not a measurement — it clears Safari's collapsing URL bar
 * without needing a real keyboard API.
 */
export const useKeyboardOpen = (): boolean => {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (Platform.OS === 'web') {
      const vv = typeof window !== 'undefined' ? window.visualViewport : null;
      if (!vv) return;
      const sync = () => setOpen(window.innerHeight - vv.height > 150);
      sync();
      vv.addEventListener('resize', sync);
      return () => vv.removeEventListener('resize', sync);
    }
    const show = Keyboard.addListener(Platform.OS === 'ios' ? 'keyboardWillShow' : 'keyboardDidShow', () => setOpen(true));
    const hide = Keyboard.addListener(Platform.OS === 'ios' ? 'keyboardWillHide' : 'keyboardDidHide', () => setOpen(false));
    return () => { show.remove(); hide.remove(); };
  }, []);

  return open;
};

export default useKeyboardOpen;
