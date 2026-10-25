import { Recipe } from '../types';
import { currentLocale } from '../i18n';

// Where a saved recipe came from, in one line (BACKLOG.md 3.7). The columns behind it
// are written at save time by AddRecipeStructured; nothing rendered them until now.
//
// `source_type` is NOT NULL with a 'text' default server-side, so a plain 'text' is
// indistinguishable from "nobody recorded anything" — it renders nothing rather than
// claiming "From a conversation" about every recipe saved before this existed.

/** seriouseats.com from https://www.seriouseats.com/recipes/123. */
export function hostLabel(url: string): string {
  // RN's URL polyfill doesn't reliably expose `hostname`; this only ever handles a host.
  return url.trim().replace(/^[a-z]+:\/\//i, '').replace(/^www\./i, '').split('/')[0] || url;
}

export function originLabel(
  recipe: Pick<Recipe, 'source_type' | 'source_url' | 'created_at'>,
  t: (key: string, options?: Record<string, unknown>) => string,
): string | null {
  const from = recipe.source_url
    ? t('origin.fromHost', { host: hostLabel(recipe.source_url) })
    : recipe.source_type === 'image' ? t('origin.fromPhoto')
    : recipe.source_type === 'voice' ? t('origin.fromVoice')
    : recipe.source_type === 'import' ? t('origin.imported')
    : null;
  if (!from) return null;

  const when = recipe.created_at ? new Date(recipe.created_at) : null;
  return when && !isNaN(when.getTime())
    ? `${from} · ${when.toLocaleDateString(currentLocale(), { day: 'numeric', month: 'short' })}`
    : from;
}
