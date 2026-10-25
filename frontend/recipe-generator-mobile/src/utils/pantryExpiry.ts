/**
 * Expiry maths for the pantry (BACKLOG 7.4). Its own file, not the screen's, because it is
 * date arithmetic across local midnight — the one part of the Pantry screen that can be
 * wrong without looking wrong. See `pantryExpiry.check.ts`.
 */

/** Anything due within this many days is what the user should cook next. */
export const USE_FIRST_DAYS = 3;

/**
 * How long a standing fact is taken on trust before the Pantry screen asks again
 * (BACKLOG 16.6). A constant and not a setting: nobody wants to tune this, and a staple
 * nobody has confirmed in a month is exactly what the stock check is for.
 */
export const STOCK_CHECK_DAYS = 28;

/**
 * Whether a staple confirmed at `confirmedAt` (an ISO timestamp from the server) is due
 * a "still enough?" question. Unparseable or missing means no — the card nags only on a
 * fact it can actually date.
 */
export const stockCheckDue = (confirmedAt: string | undefined, now: Date = new Date()): boolean => {
  const at = confirmedAt ? new Date(confirmedAt).getTime() : NaN;
  return Number.isFinite(at) && now.getTime() - at >= STOCK_CHECK_DAYS * 86400000;
};

/**
 * Whole days from today to `iso` (YYYY-MM-DD): 0 is today, negative is already expired.
 *
 * Both sides are pinned to local midnight before subtracting — an item due tomorrow must
 * read as 1 day at 23:00 as well as at 08:00, which a raw now-to-midnight difference gets
 * wrong. `T00:00:00` with no zone is deliberate: it parses as local time, matching a date
 * the user typed in their own calendar.
 */
export const daysUntil = (iso: string, today: Date = new Date()): number => {
  const from = new Date(today);
  from.setHours(0, 0, 0, 0);
  const due = new Date(`${iso}T00:00:00`);
  due.setHours(0, 0, 0, 0);
  return Math.round((due.getTime() - from.getTime()) / 86400000);
};

/**
 * How a days-remaining count reads on a badge. `t` is passed in rather than read off the
 * i18n singleton so this file stays pure and its check script runs under plain node.
 */
export const expiryLabel = (days: number, t: (key: string, options?: Record<string, unknown>) => string): string =>
  days < 0 ? t('pantry.expired')
    : days === 0 ? t('pantry.today')
    : days === 1 ? t('pantry.tomorrow')
    : t('pantry.inDays', { count: days });
