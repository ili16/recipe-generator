/**
 * The two decisions a swipe makes, as pure functions.
 *
 * Split out of `hooks/useSwipe.ts` for the same reason `feedbackPrompt.ts` is split from its
 * store: no React and no react-native imports here, so `swipeGesture.check.ts` runs under
 * plain `node` like the repo's other assert scripts.
 */

export type Axis = 'x' | 'y';

/** Distance, in px, before a drag is a swipe rather than a twitch or the start of a scroll. */
export const SWIPE_SLOP = 10;

/**
 * Whether this gesture belongs to us at all.
 *
 * A `SwipeRow` lives inside a vertical `ScrollView`, so claiming the responder on any
 * movement would eat the scroll. We take it only when the drag is past the slop *and*
 * dominantly along our axis — a diagonal goes to whoever the other axis belongs to.
 */
export const shouldClaim = (axis: Axis, dx: number, dy: number, slop: number = SWIPE_SLOP): boolean => {
  const mine = axis === 'x' ? dx : dy;
  const other = axis === 'x' ? dy : dx;
  return Math.abs(mine) > slop && Math.abs(mine) > Math.abs(other);
};

/**
 * What a release means: `-1` (left / up), `1` (right / down), or `0` — snap back, nothing
 * happened. A fast flick counts even when it is short, which is what makes the gesture feel
 * like a phone rather than a drag-and-drop.
 */
export const swipeEnd = (distance: number, velocity: number, threshold: number): -1 | 0 | 1 => {
  const far = Math.abs(distance) >= threshold;
  // A flick: fast, and at least a third of the way. Velocity alone would fire on a stray
  // vertical-ish flick that shouldClaim let through.
  const flick = Math.abs(velocity) >= 0.5 && Math.abs(distance) >= threshold / 3;
  if (!far && !flick) return 0;
  return distance < 0 ? -1 : 1;
};
