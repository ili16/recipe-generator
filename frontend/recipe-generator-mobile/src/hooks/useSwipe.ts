import { useMemo, useRef } from 'react';
import { Animated, PanResponder, PanResponderInstance } from 'react-native';
import { Axis, shouldClaim, swipeEnd } from '../utils/swipeGesture';

interface Options {
  axis: Axis;
  /** How far the finger has to travel before a release counts. */
  threshold: number;
  /** Left (x) or up (y). Omitted means that direction does not move at all. */
  onLeft?: () => void;
  /** Right (x) or down (y). Omitted means that direction does not move at all. */
  onRight?: () => void;
  enabled?: boolean;
}

/**
 * The only gesture code in the app.
 *
 * `react-native-gesture-handler` and `react-native-reanimated` are both absent and both are
 * native dependencies — adding one means a dev-client rebuild on SDK 54 to buy what
 * `PanResponder` already does for a swipe this simple. The decisions it makes live in
 * `utils/swipeGesture.ts` so they can be asserted without a renderer.
 *
 * `useNativeDriver` is **false** here on purpose: the value is driven by `setValue` while the
 * finger is down, and a node the native driver owns throws on `setValue`. One small transform
 * per row on the JS thread is the cheaper end of that trade.
 */
export function useSwipe({ axis, threshold, onLeft, onRight, enabled = true }: Options): {
  panHandlers: PanResponderInstance['panHandlers'];
  offset: Animated.Value;
} {
  const offset = useRef(new Animated.Value(0)).current;
  // Handlers change every render (closures over props); the responder is built once, so it
  // reads them through a ref rather than being rebuilt and losing an in-flight gesture.
  const opts = useRef({ onLeft, onRight, enabled, threshold });
  opts.current = { onLeft, onRight, enabled, threshold };

  const responder = useMemo(() => PanResponder.create({
    // Never on *start*: a tap inside the row has to reach the row's own onPress.
    onMoveShouldSetPanResponder: (_e, g) =>
      opts.current.enabled && shouldClaim(axis, g.dx, g.dy),
    onPanResponderMove: (_e, g) => {
      const d = axis === 'x' ? g.dx : g.dy;
      // A direction with no handler does not move — the sheet has nowhere to go upwards.
      if (d < 0 && !opts.current.onLeft) return;
      if (d > 0 && !opts.current.onRight) return;
      offset.setValue(d);
    },
    onPanResponderRelease: (_e, g) => {
      const d = axis === 'x' ? g.dx : g.dy;
      const v = axis === 'x' ? g.vx : g.vy;
      const dir = swipeEnd(d, v, opts.current.threshold);
      Animated.spring(offset, { toValue: 0, useNativeDriver: false, damping: 20, stiffness: 300 }).start();
      if (dir === -1) opts.current.onLeft?.();
      if (dir === 1) opts.current.onRight?.();
    },
    // The gesture can be taken away mid-drag (a parent ScrollView winning it back). Snap home
    // rather than leaving the row parked off to one side.
    onPanResponderTerminate: () => {
      Animated.spring(offset, { toValue: 0, useNativeDriver: false, damping: 20, stiffness: 300 }).start();
    },
  }), [axis, offset]);

  return { panHandlers: responder.panHandlers, offset };
}
