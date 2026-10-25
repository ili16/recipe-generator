import AsyncStorage from '@react-native-async-storage/async-storage';
import { STORAGE_KEYS } from '../constants';
import { Recipe } from '../types';

export async function getCachedRecipes(): Promise<Recipe[] | null> {
  try {
    const raw = await AsyncStorage.getItem(STORAGE_KEYS.RECIPES);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

export function setCachedRecipes(recipes: Recipe[]): void {
  AsyncStorage.setItem(STORAGE_KEYS.RECIPES, JSON.stringify(recipes)).catch(() => {});
}

// Prepends a newly generated/imported/saved recipe to the cache so My Recipes
// shows it without a refetch.
export async function addCachedRecipe(recipe: Recipe): Promise<void> {
  const cached = (await getCachedRecipes()) ?? [];
  setCachedRecipes([recipe, ...cached]);
}
