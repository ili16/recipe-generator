import { useCallback, useEffect, useState } from 'react';
import { Platform } from 'react-native';
import apiService from '../services/apiService';
import authService from '../services/authService';
import { Collection, Recipe, RecipeDocument } from '../types';
import { useAlert } from '../context/AlertContext';
import { useLanguage } from '../context/LanguageContext';
import * as Clipboard from 'expo-clipboard';
import { shareUrl } from '../constants';
import { getCachedRecipes, setCachedRecipes } from '../utils/recipesCache';
import { buildMarkdownExport, exportFilename } from '../utils/recipeExport';
import type { VariantPreview } from '../screens/recipes/RecipePanels';

// Everything RecipesScreen knows about the saved-recipe list: loading it, keeping the
// cache in step, and the mutations (delete, vote, manual edit, AI refine, variants).
export function useRecipeLibrary() {
  const { showAlert, confirmAction } = useAlert();
  const { t } = useLanguage();
  const [recipes, setRecipes] = useState<Recipe[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  // Trash is loaded on demand — it's a side view, not part of the cached library.
  const [trash, setTrash] = useState<Recipe[]>([]);
  const [collections, setCollections] = useState<Collection[]>([]);

  // Applies a locally-known-correct recipe list (post edit/delete/vote) to both
  // screen state and the cache, without a round-trip to the API.
  const applyRecipes = useCallback((next: Recipe[]) => {
    setRecipes(next);
    setCachedRecipes(next);
  }, []);

  const loadRecipes = useCallback(async (isRefresh = false) => {
    if (isRefresh) setRefreshing(true); else setLoading(true);
    try {
      const data = await apiService.getRecipes();
      setRecipes(data);
      setCachedRecipes(data);
    } catch (error) {
      console.error('Error loading recipes:', error);
      showAlert(t('common.error'), t('library.loadFailed'), 'error');
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [showAlert, t]);

  useEffect(() => {
    (async () => {
      try {
        const authenticated = await authService.isAuthenticated();
        setIsAuthenticated(authenticated);
        if (!authenticated) return;

        // Cache is the source of truth for opening this screen - avoids a loading
        // spinner on every visit. It's only refreshed from the API on pull-to-refresh,
        // or updated directly by local mutations and by saving a new recipe.
        const cached = await getCachedRecipes();
        if (cached) setRecipes(cached);
        else await loadRecipes();
      } catch (error) {
        console.error('Error checking auth:', error);
      }
    })();
  }, []);

  // Silent background refresh — no spinner, so it doesn't fight the cache-first mount.
  const refreshQuietly = useCallback(() => {
    apiService.getRecipes().then(applyRecipes).catch(error => console.error('Error refreshing recipes:', error));
  }, [applyRecipes]);

  const remove = async (recipeId: number) => {
    const confirmed = await confirmAction(
      t('library.deleteTitle'),
      t('library.deleteBody'),
      { confirmLabel: t('common.delete'), destructive: true }
    );
    if (!confirmed) return false;

    try {
      await apiService.deleteRecipe(recipeId);
      applyRecipes(recipes.filter(r => r.id !== recipeId));
      showAlert(t('library.movedToTrash'), t('library.movedToTrashBody'), 'success');
      return true;
    } catch (error) {
      console.error('Error deleting recipe:', error);
      showAlert(t('common.error'), t('library.deleteFailed'), 'error');
      return false;
    }
  };

  const loadCollections = useCallback(async () => {
    try {
      setCollections(await apiService.getCollections());
    } catch (error) {
      console.error('Error loading collections:', error);
    }
  }, []);

  const createCollection = async (name: string): Promise<Collection | null> => {
    try {
      const created = await apiService.createCollection(name);
      // Re-creating an existing name returns that collection, so replace rather than append.
      setCollections(prev => [...prev.filter(c => c.id !== created.id), created].sort((a, b) => a.name.localeCompare(b.name)));
      return created;
    } catch (error) {
      console.error('Error creating collection:', error);
      showAlert(t('common.error'), t('library.collectionCreateFailed'), 'error');
      return null;
    }
  };

  const removeCollection = async (collection: Collection) => {
    const confirmed = await confirmAction(
      t('library.deleteCollectionTitle', { name: collection.name }),
      t('library.deleteCollectionBody'),
      { confirmLabel: t('common.delete'), destructive: true },
    );
    if (!confirmed) return false;
    try {
      await apiService.deleteCollection(collection.id);
      setCollections(prev => prev.filter(c => c.id !== collection.id));
      return true;
    } catch (error) {
      console.error('Error deleting collection:', error);
      showAlert(t('common.error'), t('library.collectionDeleteFailed'), 'error');
      return false;
    }
  };

  // Optimistic: the membership row is the only state, and a failure puts it straight back.
  const setRecipeCollection = async (collectionId: number, recipeId: number, member: boolean) => {
    const apply = (on: boolean) => setCollections(prev => prev.map(c => (
      c.id !== collectionId ? c
        : { ...c, recipe_ids: on ? [recipeId, ...c.recipe_ids.filter(id => id !== recipeId)] : c.recipe_ids.filter(id => id !== recipeId) }
    )));
    apply(member);
    try {
      await apiService.setRecipeCollection(collectionId, recipeId, member);
    } catch (error) {
      console.error('Error updating collection membership:', error);
      apply(!member);
      showAlert(t('common.error'), t('library.collectionUpdateFailed'), 'error');
    }
  };

  const loadTrash = useCallback(async () => {
    try {
      setTrash(await apiService.getTrash());
    } catch (error) {
      console.error('Error loading trash:', error);
      showAlert(t('common.error'), t('library.trashLoadFailed'), 'error');
    }
  }, [showAlert, t]);

  // Restoring puts the recipe back at the top of the library optimistically; the next
  // refresh re-sorts it by updated_at, which is where it actually belongs.
  const restore = async (recipe: Recipe) => {
    try {
      await apiService.restoreRecipe(recipe.id);
      setTrash(prev => prev.filter(r => r.id !== recipe.id));
      applyRecipes([recipe, ...recipes.filter(r => r.id !== recipe.id)]);
      return true;
    } catch (error) {
      console.error('Error restoring recipe:', error);
      showAlert(t('common.error'), t('library.restoreFailed'), 'error');
      return false;
    }
  };

  // Toggles a thumbs up/down vote on a recipe: tapping the already-active vote clears
  // it, tapping the other one switches it. Optimistic update, reverted on error.
  const vote = async (recipe: Recipe, value: 1 | -1) => {
    const prevVote = recipe.my_vote ?? null;
    const nextVote = prevVote === value ? null : value;
    applyRecipes(recipes.map(r => (r.id === recipe.id ? { ...r, my_vote: nextVote } : r)));
    try {
      if (nextVote === null) await apiService.unvoteRecipe(recipe.id);
      else await apiService.voteRecipe(recipe.id, nextVote);
    } catch (error) {
      console.error('Error voting on recipe:', error);
      applyRecipes(recipes.map(r => (r.id === recipe.id ? { ...r, my_vote: prevVote } : r)));
      showAlert(t('common.error'), t('library.voteFailed'), 'error');
    }
  };

  // Share on: issue (or reuse) the token, put the link on the clipboard, and show it.
  // Share off: revoke, which kills the link everyone already has (BACKLOG 8.4).
  const toggleShare = async (recipe: Recipe) => {
    try {
      if (recipe.share_token) {
        if (!(await confirmAction(t('library.stopSharingTitle'), t('library.stopSharingBody'),
          { confirmLabel: t('recipes.stopSharing'), destructive: true }))) return;
        await apiService.unshareRecipe(recipe.id);
        applyRecipes(recipes.map(r => (r.id === recipe.id ? { ...r, share_token: null } : r)));
        showAlert(t('library.linkRevoked'), t('library.linkRevokedBody'), 'success');
        return;
      }
      const token = await apiService.shareRecipe(recipe.id);
      applyRecipes(recipes.map(r => (r.id === recipe.id ? { ...r, share_token: token } : r)));
      const url = shareUrl(token);
      // BACKLOG 13.2 (1): what lands in the paste is the dish and the link, not a bare
      // URL nobody can read before they tap it.
      await Clipboard.setStringAsync(`${recipe.recipename}\n${url}`);
      showAlert(t('library.linkCopied'), url, 'success');
    } catch (error) {
      console.error('Error sharing recipe:', error);
      showAlert(t('common.error'), t('library.shareFailed'), 'error');
    }
  };

  // BACKLOG 13.1 — data portability. The markdown is already on the client, so this
  // never touches the API and a 500-recipe library costs one string join.
  //
  // ponytail: web gets a real file, native gets the clipboard. A file on native needs
  // expo-file-system + expo-sharing; add them when someone asks for one.
  const exportMarkdown = async (list: Recipe[]) => {
    if (list.length === 0) return;
    const markdown = buildMarkdownExport(list);
    const filename = exportFilename(list, new Date());
    try {
      if (Platform.OS === 'web') {
        const url = URL.createObjectURL(new Blob([markdown], { type: 'text/markdown' }));
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        a.click();
        URL.revokeObjectURL(url);
        showAlert(t('library.exported'), t('library.exportedFile', { name: filename }), 'success');
      } else {
        await Clipboard.setStringAsync(markdown);
        showAlert(t('library.exported'), t('library.exportedClipboard', { count: list.length }), 'success');
      }
    } catch (error) {
      console.error('Error exporting recipes:', error);
      showAlert(t('common.error'), t('library.exportFailed'), 'error');
    }
  };

  // Structured doc isn't in the list response - fetch it on demand before editing/refining.
  const ensureStructured = async (recipe: Recipe): Promise<RecipeDocument | null> => {
    if (recipe.structured) return recipe.structured;
    try {
      const full = await apiService.getRecipeById(recipe.id);
      // Merge only the document: `full` is a detail payload and carries no pantry
      // counts, so replacing the row would drop the card's pantry badge (BACKLOG 16.7).
      applyRecipes(recipes.map(r => (r.id === recipe.id ? { ...r, structured: full.structured } : r)));
      return full.structured ?? null;
    } catch {
      showAlert(t('common.error'), t('library.detailsFailed'), 'error');
      return null;
    }
  };

  // A swap is the cook's, not the recipe's (BACKLOG 17.4c): nothing about the saved
  // recipe changes, so the card is refreshed from the server rather than patched here —
  // the swapped line comes back with its amount already converted by weight.
  const setSwap = async (recipe: Recipe, sortOrder: number, replacement: string): Promise<boolean> => {
    try {
      await apiService.setIngredientSwap(recipe.id, sortOrder, replacement);
      const full = await apiService.getRecipeById(recipe.id);
      applyRecipes(recipes.map(r => (r.id === recipe.id ? full : r)));
      return true;
    } catch {
      showAlert(t('common.error'), t('library.swapFailed'), 'error');
      return false;
    }
  };

  const saveEdit = async (id: number, doc: RecipeDocument): Promise<boolean> => {
    try {
      const updated = await apiService.patchRecipe({ id, structured: doc });
      applyRecipes(recipes.map(r => (r.id === updated.id ? updated : r)));
      return true;
    } catch {
      showAlert(t('common.error'), t('library.saveFailed'), 'error');
      return false;
    }
  };

  // One-shot refine from the recipes list: a fresh agent thread each time, since there is
  // no conversation on this surface to follow up in (BACKLOG 3.13). The agent loads the
  // recipe by id and only proposes — the patch below is still the only write.
  const refine = async (recipe: Recipe, prompt: string): Promise<boolean> => {
    const doc = await ensureStructured(recipe);
    if (!doc) return false;
    try {
      const { text, document } = await apiService.chatTurn(
        `Apply this change to my saved recipe ${recipe.id} and show me the updated version. Do not save it.\n${prompt}`,
      );
      if (!document) {
        showAlert(t('library.applyFailedTitle'), text || t('library.refineFailedBody'), 'error');
        return false;
      }
      if (recipe.manually_edited) {
        const ok = await confirmAction(
          t('library.overwriteTitle'),
          t('library.overwriteBody'),
          { confirmLabel: t('recipes.apply') },
        );
        if (!ok) return false;
      }
      const updated = await apiService.patchRecipe({
        id: recipe.id, structured: document, ai_sourced: true, change_prompt: prompt,
      });
      applyRecipes(recipes.map(r => (r.id === updated.id ? updated : r)));
      return true;
    } catch {
      showAlert(t('common.error'), t('library.refineFailed'), 'error');
      return false;
    }
  };

  // Unlike Refine (which overwrites the recipe in place), a variant is a genuinely
  // different twist proposed alongside the original — it only becomes a real, separate
  // recipe if the user accepts it.
  const generateVariant = async (recipe: Recipe, hint: string): Promise<VariantPreview | null> => {
    const doc = await ensureStructured(recipe);
    if (!doc) {
      showAlert(t('common.error'), t('library.noStructuredData'), 'error');
      return null;
    }
    try {
      const result = await apiService.generateVariant(recipe.id, hint || undefined);
      if (!result.structured) {
        showAlert(t('common.error'), t('library.variantFailed'), 'error');
        return null;
      }
      return {
        recipename: result.recipename, recipe: result.recipe,
        structured: result.structured, variantOfRecipeId: result.variant_of_recipe_id,
      };
    } catch {
      showAlert(t('common.error'), t('library.variantFailed'), 'error');
      return null;
    }
  };

  const acceptVariant = async (preview: VariantPreview): Promise<boolean> => {
    try {
      const saved = await apiService.saveRecipe(
        preview.recipename, preview.recipe, undefined, preview.structured,
        undefined, undefined, preview.variantOfRecipeId,
      );
      applyRecipes([saved, ...recipes]);
      return true;
    } catch {
      showAlert(t('common.error'), t('library.variantSaveFailed'), 'error');
      return false;
    }
  };

  const loadHistory = async (recipeId: number) => {
    try {
      return await apiService.getRecipeHistory(recipeId);
    } catch {
      showAlert(t('common.error'), t('library.historyFailed'), 'error');
      return [];
    }
  };

  return {
    recipes, loading, refreshing, isAuthenticated,
    refresh: () => loadRecipes(true),
    refreshQuietly,
    remove, vote, toggleShare, exportMarkdown, ensureStructured, saveEdit, setSwap, refine, generateVariant, acceptVariant, loadHistory,
    trash, loadTrash, restore,
    collections, loadCollections, createCollection, removeCollection, setRecipeCollection,
  };
}
