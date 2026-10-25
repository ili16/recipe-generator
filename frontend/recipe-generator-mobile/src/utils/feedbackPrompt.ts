/**
 * When to ask an alpha tester how it is going, without becoming the app that nags
 * (BACKLOG 9.16).
 *
 * The numbers are not invented. They are what survey vendors publish as defaults and what
 * the platforms enforce:
 *
 * - **Milestone, never a timer.** A well-targeted event-triggered survey lands 25-40%
 *   response; an untargeted one falls below 10%. The published gate for a feature survey is
 *   "used it ~3 times", so we wait for three finished cooks — the point at which someone
 *   actually has an opinion.
 * - **Two clocks.** Dismissing buys a short quiet period (14 days is the vendor default);
 *   answering ends it for good. A dismissal is weaker evidence than an answer, so it costs
 *   less.
 * - **A hard ceiling.** Apple caps its own review prompt at 3 per year and silently drops
 *   the rest; Google's quota is deliberately undocumented. Either way you throttle yourself,
 *   so we do: three asks, ever.
 * - **No sampling.** Vendors sample at 5-10% to protect a large user base from fatigue.
 *   With a dozen friends every opinion counts, so this is the one published default we
 *   deliberately ignore.
 */
export const MIN_COOKS = 3;
export const DISMISS_COOLDOWN_DAYS = 14;
export const MAX_ASKS = 3;

const DAY_MS = 24 * 60 * 60 * 1000;

export interface FeedbackPromptState {
  /** Finished cooks seen on this device. */
  cooks: number;
  /** Epoch ms of the last time the card was shown, or 0 for never. */
  lastAskedAt: number;
  /** How many times it has been shown. */
  asks: number;
  /** They gave us a thumb. Done — we never ask again. */
  answered: boolean;
  /** They pressed "don't ask again". Permanent, by design. */
  optedOut: boolean;
}

export const emptyState: FeedbackPromptState = {
  cooks: 0, lastAskedAt: 0, asks: 0, answered: false, optedOut: false,
};

/**
 * The whole decision, as a pure function so it can be tested without a device.
 * `now` is injectable for the same reason.
 */
export function shouldAsk(s: FeedbackPromptState, now: number): boolean {
  if (s.answered || s.optedOut) return false;
  if (s.asks >= MAX_ASKS) return false;
  if (s.cooks < MIN_COOKS) return false;
  if (s.lastAskedAt && now - s.lastAskedAt < DISMISS_COOLDOWN_DAYS * DAY_MS) return false;
  return true;
}
