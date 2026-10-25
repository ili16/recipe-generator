// Date helpers shared by the meal planner's Day/Week/Month views. Deliberately plain
// Date math (no date-fns/dayjs) — the ranges involved are all "a handful of days",
// no timezone-arithmetic library needed.

// YYYY-MM-DD from local date components. Not toISOString(), which shifts by the local
// UTC offset and would misfile evening entries onto the wrong day.
export function toISODate(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

// Local midnight for a YYYY-MM-DD string. Not `new Date(iso)`, which parses a
// date-only string as UTC midnight — round-tripped through toISODate() that can land
// on the wrong calendar day depending on the local UTC offset.
export function parseISODate(iso: string): Date {
  const [y, m, d] = iso.split('-').map(Number);
  return new Date(y, m - 1, d);
}

// 0 = Sunday .. 6 = Saturday, matching Date#getDay().
const WEEKDAY_INDEX: Record<string, number> = {
  sunday: 0, monday: 1, tuesday: 2, wednesday: 3, thursday: 4, friday: 5, saturday: 6,
};
const WEEKDAY_NAMES = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday'] as const;

// Lowercase weekday name for d, matching the format meal_plan_no_food_days /
// meal_plan_no_cook_days store (the inverse of WEEKDAY_INDEX).
export function weekdayName(d: Date): typeof WEEKDAY_NAMES[number] {
  return WEEKDAY_NAMES[d.getDay()];
}

// The most recent (or same) occurrence of weekStartDay on/before d. Defaults to Monday
// for an unrecognized weekStartDay.
export function startOfWeek(d: Date, weekStartDay: string): Date {
  const start = WEEKDAY_INDEX[weekStartDay] ?? 1;
  const day = d.getDay();
  const diff = (day - start + 7) % 7;
  const result = new Date(d);
  result.setDate(d.getDate() - diff);
  return result;
}

export function addDays(d: Date, n: number): Date {
  const copy = new Date(d);
  copy.setDate(copy.getDate() + n);
  return copy;
}

export function addMonths(d: Date, n: number): Date {
  const copy = new Date(d);
  copy.setMonth(copy.getMonth() + n);
  return copy;
}

// The full-week grid range that fully tiles the month containing d, per weekStartDay —
// the start of the week holding the 1st, through the end of the week holding the last
// day.
export function monthGridRange(d: Date, weekStartDay: string): { start: Date; end: Date } {
  const firstOfMonth = new Date(d.getFullYear(), d.getMonth(), 1);
  const lastOfMonth = new Date(d.getFullYear(), d.getMonth() + 1, 0);
  return { start: startOfWeek(firstOfMonth, weekStartDay), end: addDays(startOfWeek(lastOfMonth, weekStartDay), 6) };
}
