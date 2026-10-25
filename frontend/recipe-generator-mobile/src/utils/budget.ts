// The monthly LLM budget, rendered (BACKLOG.md 10.5). Shared by Profile, which states the
// cap, and the chat thread, which names it when a turn is refused — one spelling of the
// number and the date, or the two surfaces would disagree about when the cap lifts.
// Locale is passed in rather than read here, so this file stays free of the i18n runtime
// and its self-check can run as plain node.

export const formatUSD = (usd: number, locale: string) =>
  new Intl.NumberFormat(locale, { style: 'currency', currency: 'USD' }).format(usd);

// The cap is per calendar month, so it lifts on the first of the next one — which is a
// January the caller has to be given, not a 13th month of the same year.
export const resetsOn = (periodStart: string, locale: string) => {
  const start = new Date(periodStart);
  return new Date(start.getUTCFullYear(), start.getUTCMonth() + 1, 1)
    .toLocaleDateString(locale, { day: 'numeric', month: 'long' });
};
