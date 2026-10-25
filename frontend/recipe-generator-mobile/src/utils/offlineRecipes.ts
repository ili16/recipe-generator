import { Recipe } from '../types';

// BACKLOG 12.4 — the kitchen has bad wifi.
//
// Scoped to exactly the one screen that must work without a network: cooking mode reads
// a recipe's *structured* document, and the library list does not carry one (it is
// fetched per recipe, on demand). So the cache is full recipe documents for the ones you
// are most likely to stand at a hob with: whatever you last opened, plus whatever is
// planned. Read-only. Not sync, not conflict resolution, not an offline agent — every AI
// path still says it needs a connection.

/** Last opened + a planned week, comfortably. An unbounded cache of every recipe ever
 *  opened is a phone-storage bug, not an offline feature. */
export const COOK_CACHE_MAX = 25;

/** Newest first, one entry per recipe id, capped. `fresh` wins over a cached copy of
 *  the same recipe — it is the one that just came off the server. */
export function mergeCookCache(cached: Recipe[], fresh: Recipe[], max = COOK_CACHE_MAX): Recipe[] {
  const seen = new Set<number>();
  const out: Recipe[] = [];
  for (const r of [...fresh, ...cached]) {
    if (seen.has(r.id)) continue;
    seen.add(r.id);
    out.push(r);
    if (out.length === max) break;
  }
  return out;
}

/** An axios failure with no HTTP status never reached the server — which, on a phone in
 *  a kitchen, means the wifi. A 5xx is the server being broken and is a different
 *  message. */
export function isOffline(err: unknown): boolean {
  return !!err && typeof err === 'object' && 'status' in err && (err as { status?: number }).status === undefined;
}
