import AsyncStorage from '@react-native-async-storage/async-storage';
import authService from '../services/authService';
import { cookCacheKey } from '../constants';
import { Recipe } from '../types';
import { mergeCookCache } from './offlineRecipes';

// The AsyncStorage half of the cooking-mode offline cache (BACKLOG 12.4). Split from
// `offlineRecipes.ts` so the decisions there stay runnable outside React Native, the way
// pantryExpiry/pantryViewStore already are.

// Keyed by user like the meal-plan cache: a shared phone must never hand one account's
// recipes to another.
async function key(): Promise<string | null> {
  const userId = await authService.getUserId();
  return userId ? cookCacheKey(userId) : null;
}

async function read(): Promise<Recipe[]> {
  try {
    const k = await key();
    if (!k) return [];
    const raw = await AsyncStorage.getItem(k);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

/** Keep these recipes available offline. Fire-and-forget: a cache write must never fail
 *  the request that produced the data. */
export async function rememberForOffline(recipes: Recipe[]): Promise<void> {
  const withDoc = recipes.filter(r => r.structured);
  if (withDoc.length === 0) return;
  try {
    const k = await key();
    if (!k) return;
    await AsyncStorage.setItem(k, JSON.stringify(mergeCookCache(await read(), withDoc)));
  } catch {
    /* a full or unavailable disk is not worth a message the cook cannot act on */
  }
}

export async function offlineRecipe(id: number): Promise<Recipe | null> {
  return (await read()).find(r => r.id === id) ?? null;
}

/** Which of these ids are not cached yet — the prefetch only spends requests on those. */
export async function missingFromOffline(ids: number[]): Promise<number[]> {
  const have = new Set((await read()).map(r => r.id));
  return ids.filter(id => !have.has(id));
}
