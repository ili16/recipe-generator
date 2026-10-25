import { RecipeDocument } from '../types';

export type Ingredient = RecipeDocument['ingredients'][number];

// Round to 2dp and drop the trailing zeros, the same shape PantryScreen's amountLabel and
// GroceryListScreen's formatAmount use. 1.5 stays 1.5; 1.4999999999 does not leak out.
const fmtQty = (n: number): string => String(Math.round(n * 100) / 100);

// One ingredient as a line: "250 g flour". quantity_text wins when the model wrote an
// amount in words rather than a number ("a pinch of"). Lives here rather than in
// RecipeView because it is a pure string function that non-rendering code — the version
// diff, its assert script — needs too.
//
// showGrams appends the weight of a countable line ("1.5 onion (~225 g)"). It is off by
// default and only the servings scaler turns it on: half an onion is a number a cook
// needs help with, but the same hint in a version diff is noise, and the diff renders
// through this function too.
export const fmtIngredient = (ing: Ingredient, showGrams = false): string => {
  if (ing.quantity_text) return `${ing.quantity_text} ${ing.item}`;
  if (ing.quantity == null) return ing.item;

  const line = `${fmtQty(ing.quantity)}${ing.unit ? ' ' + ing.unit : ''} ${ing.item}`;
  // Only countable lines get the hint, and it goes after the food, not after the number:
  // "1.5 onion (~225 g)". A line already written by weight does not need one, and a whole
  // number of onions is not the case anybody is stuck on.
  const rounded = Math.round(ing.quantity * 100) / 100;
  if (showGrams && !ing.unit && ing.grams_per_unit != null && !Number.isInteger(rounded)) {
    return `${line} (~${fmtQty(rounded * ing.grams_per_unit)} g)`;
  }
  return line;
};

// scaleIngredient multiplies the numeric amount and nothing else (BACKLOG 17.3).
//
// quantity_text is never scaled: "a pinch" does not become "1.5 pinches", and "to taste"
// has no factor at all. That is the whole reason the schema keeps the two apart.
//
// Returns the ingredient untouched at factor 1 so a recipe nobody scaled renders through
// exactly the same object it always did.
export const scaleIngredient = (ing: Ingredient, factor: number): Ingredient =>
  factor === 1 || ing.quantity == null ? ing : { ...ing, quantity: ing.quantity * factor };

// servingScale is the client's copy of model.PortionScale: how much of a recipe the cook
// wants relative to what it yields. Unknown or nonsensical servings on either side mean
// "as written", because guessing is worse than not scaling.
export const servingScale = (want: number | null | undefined, yields: number | null | undefined): number =>
  want == null || yields == null || want <= 0 || yields <= 0 ? 1 : want / yields;
