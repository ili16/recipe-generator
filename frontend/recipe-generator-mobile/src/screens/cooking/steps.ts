import { CookPhase, RecipeDocument } from '../../types';

export type Step = RecipeDocument['steps'][number];
export type Ingredient = RecipeDocument['ingredients'][number];

// One card in cooking mode: either a single step, or the several steps a cook does at the
// same time. Grouping is what makes "boil the water while you chop" one thing you stand
// and do rather than two screens that teach you to cook it slowly.
export interface StepGroup {
  steps: Step[];
  /** The group's phase, or null when the steps carry none (everything pre-cook-flow). */
  phase: CookPhase | null;
  /** Indices into the original steps array — notes stay keyed on these. */
  indices: number[];
  /**
   * How long the block takes: the longest track, not the sum. Two things happening at
   * once take as long as the slower one. null when no track has a timer.
   */
  timerSeconds: number | null;
}

export const phaseLabelKey = (phase: CookPhase) => `cooking.phase.${phase}`;

// Fold consecutive steps sharing a non-null parallel_group into one card; everything else
// is a card of its own. A step list with no groups at all (every recipe saved before the
// cook flow existed) comes back one-to-one, which is exactly the old behaviour.
export function groupSteps(steps: Step[]): StepGroup[] {
  const groups: StepGroup[] = [];
  for (let i = 0; i < steps.length; i++) {
    const step = steps[i];
    const g = step.parallel_group;
    const last = groups[groups.length - 1];
    // Only *consecutive* steps fold: the same group number reused later in the recipe is
    // a separate block of parallel work, not a continuation of the earlier one.
    if (last !== undefined && g != null && last.steps[0].parallel_group === g) {
      last.steps.push(step);
      last.indices.push(i);
      last.phase = last.phase ?? step.phase ?? null;
      last.timerSeconds = maxTimer(last.timerSeconds, step.timer_seconds);
      continue;
    }
    groups.push({
      steps: [step],
      phase: step.phase ?? null,
      indices: [i],
      timerSeconds: step.timer_seconds ?? null,
    });
  }
  return groups;
}

function maxTimer(a: number | null, b: number | null | undefined): number | null {
  if (b == null) return a;
  return a == null ? b : Math.max(a, b);
}

// What the group is called while cooking it. A single step keeps its own text as the
// heading; a parallel block needs a name of its own, since neither track is the title.
// `t` is passed in: this file is pure, and its check script runs under plain node.
export function groupSpokenText(group: StepGroup, t: (key: string) => string): string {
  if (group.steps.length === 1) return group.steps[0].step_text;
  return [
    t('cooking.atTheSameTime'),
    ...group.steps.map((s, i) => `${i + 1}. ${s.step_text}`),
  ].join(' ');
}

// Total time per phase across the whole recipe, for the overview's summary. Parallel
// blocks count once (their longest track), which is the honest number: that is how long
// the cook actually stands there.
export function phaseSummary(groups: StepGroup[]): Array<{ phase: CookPhase; groups: number; seconds: number }> {
  const order: CookPhase[] = ['prep', 'cook', 'wait'];
  return order
    .map(phase => {
      const mine = groups.filter(g => g.phase === phase);
      return {
        phase,
        groups: mine.length,
        seconds: mine.reduce((sum, g) => sum + (g.timerSeconds ?? 0), 0),
      };
    })
    .filter(p => p.groups > 0);
}

// Which of the recipe's ingredients a step uses — as INDICES into `allIngredients`, not
// copies: cooking mode ticks them off (BACKLOG 12.1) and a tick has to mean "this one",
// the same ingredient wherever else it shows up. Use DB-provided indices if available,
// fall back to text matching.
export function getStepIngredientIndices(step: Step, allIngredients: Ingredient[]): number[] {
  if (step.ingredient_indices && step.ingredient_indices.length > 0) {
    return step.ingredient_indices.filter(i => i >= 0 && i < allIngredients.length);
  }
  // Fallback for recipes without DB-backed indices.
  const lower = step.step_text.toLowerCase();
  return allIngredients.flatMap((ing, i) => {
    const words = ing.item.toLowerCase().split(/\s+/).filter(w => w.length > 3 && !/^\d/.test(w));
    return words.length > 0 && words.some(w => lower.includes(w)) ? [i] : [];
  });
}

// A running timer counts in mm:ss and never in "3 min" — a cook watching the last thirty
// seconds of a sear needs the seconds (BACKLOG 12.3). `formatTimer` below is the resting
// label, this is the live one.
export function formatCountdown(msLeft: number): string {
  const total = Math.max(0, Math.ceil(msLeft / 1000));
  const m = Math.floor(total / 60);
  return `${m}:${String(total % 60).padStart(2, '0')}`;
}

export function formatTimer(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return s > 0 ? `${m}m ${s}s` : `${m} min`;
}
