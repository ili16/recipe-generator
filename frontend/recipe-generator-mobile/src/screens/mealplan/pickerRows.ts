import { Recipe, MealSlot } from '../../types';
import { SLOT_TAGS } from '../../constants/tags';

/** A picker list is recipes plus at most one "Other recipes" divider. */
export type PickerRow =
  | { kind: 'recipe'; recipe: Recipe }
  | { kind: 'heading'; labelKey: string };

const tagsOf = (r: Recipe): string[] => r.tags ?? r.structured?.tags ?? [];

/**
 * Order the saved library for the slot being filled (BACKLOG 9.9).
 *
 * Ranked, never filtered — people eat pizza for breakfast, so a recipe that isn't tagged for
 * the slot moves down rather than disappearing. The divider is only emitted when both sides
 * of it are non-empty; a heading over an empty list, or over the whole list, says nothing.
 */
export function pickerRows(recipes: Recipe[], slot: MealSlot | undefined, query: string): PickerRow[] {
  const q = query.trim().toLowerCase();
  const matching = q ? recipes.filter(r => r.recipename.toLowerCase().includes(q)) : recipes;
  const flat = (rs: Recipe[]): PickerRow[] => rs.map(recipe => ({ kind: 'recipe', recipe }));

  const wanted = slot ? SLOT_TAGS[slot] ?? [] : [];
  if (wanted.length === 0) return flat(matching);

  const likely: Recipe[] = [];
  const rest: Recipe[] = [];
  for (const r of matching) {
    (tagsOf(r).some(t => wanted.includes(t)) ? likely : rest).push(r);
  }
  if (likely.length === 0 || rest.length === 0) return flat(matching);
  return [...flat(likely), { kind: 'heading', labelKey: 'plan.otherRecipes' }, ...flat(rest)];
}
