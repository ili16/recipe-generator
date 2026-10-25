import type { MealPlanItem, Recipe, Weekday } from '../../types';
import { addDays, toISODate, weekdayName } from '../../utils/mealPlanDates';
import type { PlannedDay, PlannedMeal } from './DayCard';

/**
 * Turn seven dates plus the plan cache into what the week view renders.
 *
 * **A leftover says so.** `meal_plan_items.source_item_id` (BACKLOG 9.11) names the item whose
 * pot this meal eats again; the backend sets it when it carries a dish over a no-cook day
 * (`enforceNoCookCarryover`) and the batch-cook control sets it for the days one pot covers.
 * This used to be inferred — "the day before held this same `recipe_id`" — which is wrong
 * whenever reality is: cook the same dish twice on purpose and the second day was labelled
 * leftovers with no way to correct it.
 *
 * Which *kind* of repeat it is stays derived from `meal_plan_no_cook_days`: that is genuinely a
 * labelling question ("a day you chose not to cook" vs "a day you cooked ahead for"), not a
 * data one.
 *
 * `locale` is passed in rather than read off the i18n singleton, so this file stays pure and
 * `planDays.check.ts` keeps running under plain node.
 *
 * `priorDay` is the day *before* the window — a Monday's leftovers can point at Sunday's cook,
 * and without it that row cannot name the day it came from.
 */
export function buildWeek(args: {
  weekStart: Date;
  today: Date;
  itemsByDate: Record<string, MealPlanItem[]>;
  priorDay: MealPlanItem[];
  recipesById: Record<number, Recipe>;
  noCookDays: Weekday[];
  locale: string;
}): PlannedDay[] {
  const { weekStart, today, itemsByDate, priorDay, recipesById, noCookDays, locale } = args;
  const todayISO = toISODate(today);

  // Everything the cache holds, not just this week: a leftover names its cook by id, and the
  // cook may sit outside the seven days on screen.
  const all = [...priorDay, ...Object.values(itemsByDate).flat()];
  const byID: Record<number, MealPlanItem> = {};
  for (const item of all) byID[item.id] = item;

  // Portions of one pot, in the order they are eaten. The cook is portion 1, so a leftover's
  // own position is its index here + 2.
  const siblings: Record<number, number[]> = {};
  for (const item of [...all].sort(byDayThenSlot)) {
    const src = item.source_item_id;
    if (src != null) siblings[src] = [...(siblings[src] ?? []), item.id];
  }

  return Array.from({ length: 7 }, (_, i) => {
    const date = addDays(weekStart, i);
    const iso = toISODate(date);
    const items = itemsByDate[iso] ?? [];
    const weekday = date.toLocaleDateString(locale, { weekday: 'long' });
    const isNoCookDay = noCookDays.includes(weekdayName(date));

    const meals: PlannedMeal[] = items.map(item => {
      const source = item.source_item_id != null ? byID[item.source_item_id] : undefined;
      const portions = item.source_item_id != null ? siblings[item.source_item_id] ?? [] : [];
      return {
        item,
        slot: item.meal_slot,
        kind: item.source_item_id == null ? 'cook' : isNoCookDay ? 'leftover' : 'batch',
        carriedFrom: source ? weekdayLabel(source.planned_on, locale) : undefined,
        // "Portion 2 of 3": how much is cooked belongs to the cooking day, so this is read,
        // never edited, on a leftover row (BACKLOG 9.11).
        portion: portions.length
          ? { index: portions.indexOf(item.id) + 2, total: portions.length + 1 }
          : undefined,
        // How many *other* days this pot covers, so a cook that feeds a household for
        // three days is not reported as cooking twice too much (BACKLOG 9.12).
        covers: (siblings[item.id] ?? []).length,
        // A leftover is not cooked itself — the pot it came from was. Reading the source's
        // mark is what makes a batch-cooked run go quiet together instead of the cooking
        // day alone going grey while its leftovers still look outstanding.
        cooked: (source ?? item).cooked_at != null,
        title: item.recipe_title,
        servings: item.servings ?? null,
        recipe: recipesById[item.recipe_id],
      };
    });

    return {
      iso,
      weekday,
      dateLabel: date.toLocaleDateString(locale, { month: 'short', day: 'numeric' }),
      isToday: iso === todayISO,
      isPast: iso < todayISO,
      meals,
    };
  });
}

const SLOT_ORDER = { breakfast: 0, lunch: 1, dinner: 2, snack: 3 } as const;

const byDayThenSlot = (a: MealPlanItem, b: MealPlanItem): number =>
  a.planned_on.localeCompare(b.planned_on) || SLOT_ORDER[a.meal_slot] - SLOT_ORDER[b.meal_slot];

const weekdayLabel = (iso: string, locale: string): string => {
  const [y, m, d] = iso.split('-').map(Number);
  return new Date(y, m - 1, d).toLocaleDateString(locale, { weekday: 'long' });
};
