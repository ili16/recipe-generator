import AsyncStorage from '@react-native-async-storage/async-storage';
import { FeedbackPromptState, emptyState } from './feedbackPrompt';

/**
 * Persistence for the pulse prompt (BACKLOG 9.16). Split from `feedbackPrompt.ts` so the
 * rules there stay a pure function with no imports — that is what lets
 * `feedbackPrompt.check.ts` run under plain `node` like the repo's other assert scripts.
 *
 * Same shape as `utils/recipesCache.ts`: try/catch reads, fire-and-forget writes.
 */

/**
 * Deliberately not in `STORAGE_KEYS`: `authService.clearSession()` wipes those on logout,
 * which would turn signing out into a reset button on the nag.
 */
const FEEDBACK_PROMPT_KEY = 'feedback_prompt_v1';

export async function readPromptState(): Promise<FeedbackPromptState> {
  try {
    const raw = await AsyncStorage.getItem(FEEDBACK_PROMPT_KEY);
    return raw ? { ...emptyState, ...JSON.parse(raw) } : emptyState;
  } catch {
    return emptyState;
  }
}

async function update(change: Partial<FeedbackPromptState>): Promise<void> {
  const next = { ...(await readPromptState()), ...change };
  AsyncStorage.setItem(FEEDBACK_PROMPT_KEY, JSON.stringify(next)).catch(() => {});
}

/** Called when a recipe is marked cooked — the milestone the prompt rides on. */
export async function recordCook(): Promise<void> {
  const s = await readPromptState();
  if (s.answered || s.optedOut) return; // nothing left to count towards
  await update({ cooks: s.cooks + 1 });
}

/** The card was shown. Starts the short clock and spends one of the three asks. */
export async function recordAsked(now = Date.now()): Promise<void> {
  const s = await readPromptState();
  await update({ lastAskedAt: now, asks: s.asks + 1 });
}

/** They tapped a thumb. That is the answer; we stop. */
export async function recordAnswered(): Promise<void> {
  await update({ answered: true });
}

/** They asked us to stop. Permanent, not a long cooldown. */
export async function recordOptOut(): Promise<void> {
  await update({ optedOut: true });
}
