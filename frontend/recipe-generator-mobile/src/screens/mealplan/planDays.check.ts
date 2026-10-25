/**
 * Self-check for `buildWeek` — the only real logic in the planner, and the piece that decides
 * whether a repeated dish reads as deliberate or as a bug.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/screens/mealplan/planDays.check.ts --outDir /tmp/pd --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/pd/screens/mealplan/planDays.check.js
 *
 * (tsc reports TS6142 for the type-only `./DayCard` import and emits anyway — that is expected.)
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import type { MealPlanItem, MealSlot } from '../../types';
import { buildWeek } from './planDays';

let nextID = 1;
const item = (iso: string, recipeId: number, title: string, slot: MealSlot = 'dinner'): MealPlanItem =>
  ({ id: nextID++, recipe_id: recipeId, recipe_title: title, planned_on: iso, meal_slot: slot });

/** The same, but explicitly the leftovers of `source` — what the backend now stores. */
const leftoverOf = (source: MealPlanItem, iso: string, slot: MealSlot = 'dinner'): MealPlanItem =>
  ({ ...item(iso, source.recipe_id, source.recipe_title, slot), source_item_id: source.id });

const weekStart = new Date(2026, 8, 7); // Mon 2026-09-07 … Sun 2026-09-13
const today = new Date(2026, 8, 9); // Wednesday

const run = (items: MealPlanItem[], opts: { priorDay?: MealPlanItem[]; noCookDays?: string[] } = {}) => {
  const itemsByDate: Record<string, MealPlanItem[]> = {};
  for (const i of items) itemsByDate[i.planned_on] = [...(itemsByDate[i.planned_on] ?? []), i];
  return buildWeek({
    locale: 'en',
    weekStart,
    today,
    itemsByDate,
    priorDay: opts.priorDay ?? [],
    recipesById: {},
    noCookDays: (opts.noCookDays ?? []) as never[],
  });
};

// Every assertion below is about the single meal of a day unless it says otherwise.
const only = (day: { meals: { kind: string; carriedFrom?: string; portion?: unknown }[] }) => day.meals[0];
const kindOf = (day: { meals: { kind: string }[] }) => day.meals[0]?.kind ?? 'empty';

export function check(): void {
  // A leftover is a leftover because it says so, and names the day it came from.
  let cook = item('2026-09-07', 1, 'Roast');
  let d = run([cook, leftoverOf(cook, '2026-09-08')], { noCookDays: ['tuesday'] });
  assert.equal(kindOf(d[0]), 'cook');
  assert.equal(kindOf(d[1]), 'leftover');
  assert.equal(only(d[1]).carriedFrom, d[0].weekday);

  // The same link with no no-cook preference is a batch day, not a leftover — that half
  // is still a labelling question, and still the user's own preference answering it.
  cook = item('2026-09-07', 1, 'Roast');
  d = run([cook, leftoverOf(cook, '2026-09-08')]);
  assert.equal(kindOf(d[1]), 'batch');

  // BACKLOG 9.11's whole point: the same dish planned twice *on purpose* is two cooks.
  // The old heuristic called the second one leftovers and gave you no way to say otherwise.
  d = run([item('2026-09-07', 1, 'Roast'), item('2026-09-08', 1, 'Roast')], { noCookDays: ['tuesday'] });
  assert.deepEqual(d.slice(0, 2).map(kindOf), ['cook', 'cook']);

  // Every leftover points at the cook, not at the previous leftover, so a run of them all
  // trace back to the day actually cooked — and each knows which portion it is.
  cook = item('2026-09-07', 1, 'Roast');
  d = run(
    [cook, leftoverOf(cook, '2026-09-08'), leftoverOf(cook, '2026-09-09')],
    { noCookDays: ['tuesday', 'wednesday'] },
  );
  assert.deepEqual(d.slice(0, 3).map(kindOf), ['cook', 'leftover', 'leftover']);
  assert.equal(only(d[2]).carriedFrom, d[0].weekday);
  assert.deepEqual(only(d[1]).portion, { index: 2, total: 3 });
  assert.deepEqual(only(d[2]).portion, { index: 3, total: 3 });

  // A different recipe is a fresh cook even on a no-cook day.
  d = run([item('2026-09-07', 1, 'Roast'), item('2026-09-08', 2, 'Curry')], { noCookDays: ['tuesday'] });
  assert.equal(kindOf(d[1]), 'cook');

  // The window's first day can be leftovers of the day *before* the window.
  const sunday = item('2026-09-06', 1, 'Roast');
  d = run([leftoverOf(sunday, '2026-09-07')], { priorDay: [sunday], noCookDays: ['monday'] });
  assert.equal(kindOf(d[0]), 'leftover');
  assert.equal(only(d[0]).carriedFrom, 'Sunday');

  // Past days are dimmed, today is marked.
  d = run([]);
  assert.deepEqual(d.map(x => x.isPast), [true, true, false, false, false, false, false]);
  assert.equal(d[2].isToday, true);

  // BACKLOG 6.4: a day holds a meal per slot. Both survive, in the order the day is
  // eaten, and each is classified on its own.
  cook = item('2026-09-07', 1, 'Roast');
  d = run([
    cook,
    item('2026-09-08', 2, 'Porridge', 'breakfast'),
    leftoverOf(cook, '2026-09-08', 'lunch'),
    item('2026-09-08', 3, 'Curry'),
  ], { noCookDays: ['tuesday'] });
  assert.deepEqual(d[1].meals.map(m => m.slot), ['breakfast', 'lunch', 'dinner']);
  assert.deepEqual(d[1].meals.map(m => m.kind), ['cook', 'leftover', 'cook']);
  assert.equal(d[1].meals[1].carriedFrom, 'Monday');

  // Lunch eaten again as that evening's dinner: same day, still a link, still a repeat.
  const lunch = item('2026-09-07', 1, 'Roast', 'lunch');
  d = run([lunch, leftoverOf(lunch, '2026-09-07')]);
  assert.deepEqual(d[0].meals.map(m => m.kind), ['cook', 'batch']);
  assert.equal(d[0].meals[1].carriedFrom, 'Monday');

  console.log('planDays: all 10 checks pass');
}

check();
