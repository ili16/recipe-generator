// The thread list, grouped and filtered (BACKLOG.md 10.7). A flat list of a hundred
// titles is not scannable; "Today / Yesterday / 12 Sept" is.
//
// Labels and locale are passed in rather than read here, so this file stays free of the
// i18n runtime and its self-check can run as plain node.

export interface ThreadLike {
  title: string;
  preview: string;
  updated_at: string;
}

export interface ThreadGroup<T> {
  label: string;
  threads: T[];
}

// Search is over what the row actually shows — the title, or the preview standing in for
// a thread the naming call never got to. Case- and accent-insensitive, so "grunkohl"
// finds "Grünkohl"; full-text over messages is a separate entry if anyone asks.
const fold = (s: string) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();

export const matchesQuery = (t: ThreadLike, query: string) => {
  const q = fold(query.trim());
  return q === '' || fold(t.title).includes(q) || fold(t.preview).includes(q);
};

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

/**
 * Groups threads by the *local* calendar day they were last used, preserving the order
 * they arrive in (the server already sorts newest first). Day boundaries are local, not
 * 24-hour windows: a thread from 23:00 last night is "Yesterday" at 08:00, not "Today".
 */
export function groupThreads<T extends ThreadLike>(
  threads: T[],
  opts: { now: Date; locale: string; today: string; yesterday: string },
): ThreadGroup<T>[] {
  const today = startOfDay(opts.now);
  const day = 86400000;

  const out: ThreadGroup<T>[] = [];
  for (const t of threads) {
    const at = new Date(t.updated_at);
    const start = startOfDay(at);
    const label =
      start === today ? opts.today
      : start === today - day ? opts.yesterday
      // A date from this year needs no year; an older one does, or "12 Sept" is a lie.
      : at.toLocaleDateString(opts.locale, {
          day: 'numeric', month: 'short',
          ...(at.getFullYear() === opts.now.getFullYear() ? {} : { year: 'numeric' }),
        });
    if (out.length > 0 && out[out.length - 1].label === label) out[out.length - 1].threads.push(t);
    else out.push({ label, threads: [t] });
  }
  return out;
}
