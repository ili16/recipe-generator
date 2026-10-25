import React, { useState, useEffect, useMemo } from 'react';
import { View, Text, StyleSheet, ScrollView, TouchableOpacity, RefreshControl } from 'react-native';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import Loading from '../components/Loading';
import { useTheme, Theme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { type } from '../theme';
import { useEscapeBack } from '../hooks/useEscapeBack';
import { useRecipeLibrary } from '../hooks/useRecipeLibrary';
import RecipeToolbar, { SortMode } from './recipes/RecipeToolbar';
import RecipeCard from './recipes/RecipeCard';
import { QuickFilter, matchesQuickFilters, pantryRatio } from './recipes/quickFilters';
import { Button, Card, Text as UIText } from '../components/ui';

type Props = NativeStackScreenProps<RootStackParamList, 'Recipes'>;

const RecipesScreen: React.FC<Props> = ({ navigation }) => {
  const lib = useRecipeLibrary();
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [search, setSearch] = useState('');
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());
  const [sortMode, setSortMode] = useState<SortMode>('recent');
  const [trashMode, setTrashMode] = useState(false);
  const [activeCollectionId, setActiveCollectionId] = useState<number | null>(null);
  const [quickFilters, setQuickFilters] = useState<Set<QuickFilter>>(new Set());
  const { theme } = useTheme();
  const { t } = useLanguage();
  const styles = useMemo(() => makeStyles(theme), [theme]);
  useEscapeBack();

  // Keep the list current with whatever created a recipe elsewhere (e.g. accepting an
  // AI-suggested meal-plan variant) — a silent background refresh, no spinner, so this
  // doesn't fight the cache-first mount.
  useEffect(() => navigation.addListener('focus', lib.refreshQuietly), [navigation]);

  useEffect(() => { if (lib.isAuthenticated) lib.loadCollections(); }, [lib.isAuthenticated, lib.loadCollections]);

  const toggleTagFilter = (slug: string) => {
    setSelectedTags(prev => {
      const next = new Set(prev);
      if (next.has(slug)) next.delete(slug); else next.add(slug);
      return next;
    });
  };

  const toggleQuickFilter = (f: QuickFilter) => {
    setQuickFilters(prev => {
      const next = new Set(prev);
      if (next.has(f)) next.delete(f); else next.add(f);
      return next;
    });
  };

  const recipeNameById = useMemo(() => {
    const map = new Map<number, string>();
    // A fork of a household member's recipe says whose it was — the parent name alone
    // reads as if it were yours (15.7).
    lib.recipes.forEach(r => map.set(r.id, r.owned_by_me === false
      ? t('recipes.byOwner', { name: r.recipename, owner: r.owner_name || t('household.unnamedMember') })
      : r.recipename));
    return map;
  }, [lib.recipes, t]);

  // How many saved variants each parent recipe has — the list already carries every
  // variant's parent link, so this is a tally, not a query (BACKLOG 6.5).
  const variantCountByParent = useMemo(() => {
    const counts = new Map<number, number>();
    lib.recipes.forEach(r => {
      if (r.variant_of_recipe_id != null) {
        counts.set(r.variant_of_recipe_id, (counts.get(r.variant_of_recipe_id) ?? 0) + 1);
      }
    });
    return counts;
  }, [lib.recipes]);

  const visibleRecipes = useMemo(() => {
    const q = search.trim().toLowerCase();
    const inCollection = activeCollectionId === null
      ? null
      : new Set(lib.collections.find(c => c.id === activeCollectionId)?.recipe_ids ?? []);
    let list = lib.recipes.filter(r => {
      const matchesSearch = !q ||
        r.recipename.toLowerCase().includes(q) ||
        r.recipe.toLowerCase().includes(q);
      const matchesTags = selectedTags.size === 0 ||
        Array.from(selectedTags).every(slug => (r.tags ?? []).includes(slug));
      return matchesSearch && matchesTags && matchesQuickFilters(r, quickFilters) &&
        (inCollection === null || inCollection.has(r.id));
    });
    if (sortMode === 'name') {
      list = [...list].sort((a, b) => a.recipename.localeCompare(b.recipename));
    }
    if (sortMode === 'pantry') {
      // Stable, so ties keep the recent-first order the server sent.
      list = [...list].sort((a, b) => pantryRatio(b) - pantryRatio(a) || (b.pantry_have ?? 0) - (a.pantry_have ?? 0));
    }
    return list;
  }, [lib.recipes, lib.collections, search, selectedTags, sortMode, activeCollectionId, quickFilters]);

  if (!lib.isAuthenticated) {
    return (
      <View style={styles.container}>
        <View style={styles.emptyContainer}>
          <Text style={styles.emptyText}>{t('recipes.signInRequired')}</Text>
          <Text style={styles.emptySubtext}>{t('recipes.signInBlurb')}</Text>
          <TouchableOpacity style={styles.generateButton} onPress={() => navigation.navigate('Login')}>
            <Text style={styles.generateButtonText}>{t('nav.login')}</Text>
          </TouchableOpacity>
          <TouchableOpacity style={styles.skipButton} onPress={() => navigation.navigate('Chat')}>
            <Text style={styles.skipButtonText}>{t('recipes.generateAnonymously')}</Text>
          </TouchableOpacity>
        </View>
      </View>
    );
  }

  if (!trashMode && lib.loading && lib.recipes.length === 0) {
    return (
      <View style={styles.container}>
        <Loading visible={true} message={t('recipes.loading')} />
      </View>
    );
  }

  if (!trashMode && lib.recipes.length === 0) {
    return (
      <View style={styles.container}>
        <View style={styles.emptyContainer}>
          <Text style={styles.emptyText}>{t('recipes.emptyTitle')}</Text>
          <Text style={styles.emptySubtext}>{t('recipes.emptyBlurb')}</Text>
          <TouchableOpacity style={styles.generateButton} onPress={() => navigation.navigate('Chat')}>
            <Text style={styles.generateButtonText}>{t('recipes.generateRecipe')}</Text>
          </TouchableOpacity>
        </View>
      </View>
    );
  }

  return (
    <View style={styles.container}>
      <RecipeToolbar
        search={search}
        onSearch={setSearch}
        selectedTags={selectedTags}
        onToggleTag={toggleTagFilter}
        sortMode={sortMode}
        onToggleSort={() => setSortMode(m => (m === 'recent' ? 'name' : m === 'name' ? 'pantry' : 'recent'))}
        collections={lib.collections}
        activeCollectionId={activeCollectionId}
        onSelectCollection={setActiveCollectionId}
        onDeleteCollection={async c => {
          if (await lib.removeCollection(c) && activeCollectionId === c.id) setActiveCollectionId(null);
        }}
        quickFilters={quickFilters}
        onToggleQuickFilter={toggleQuickFilter}
        onExport={() => lib.exportMarkdown(visibleRecipes)}
        trashMode={trashMode}
        onToggleTrash={() => {
          setTrashMode(v => !v);
          if (!trashMode) lib.loadTrash();
        }}
      />


      {!trashMode && lib.recipes.length > 0 && (
        <Button title={t('nav.map')} variant="ghost" size="sm" onPress={() => navigation.navigate('Map')} style={{ marginHorizontal: 16 }} />
      )}
      {trashMode ? (
        lib.trash.length === 0 ? (
          <View style={styles.emptyContainer}>
            <UIText variant="title">{t('recipes.trashEmpty')}</UIText>
            <UIText tone="muted" style={{ marginTop: 8, textAlign: 'center' }}>
              {t('recipes.trashBlurb')}
            </UIText>
          </View>
        ) : (
          <ScrollView style={styles.scrollView} contentContainerStyle={styles.scrollContent}>
            {lib.trash.map(recipe => (
              <Card key={recipe.id} style={styles.trashRow}>
                <View style={styles.trashText}>
                  <UIText variant="label" numberOfLines={1}>{recipe.recipename}</UIText>
                  <UIText variant="caption" tone="muted">{deletedLabel(recipe.deleted_at, t)}</UIText>
                </View>
                <Button title={t('recipes.restore')} variant="secondary" size="sm" onPress={() => lib.restore(recipe)} />
              </Card>
            ))}
          </ScrollView>
        )
      ) : visibleRecipes.length === 0 ? (
        <View style={styles.emptyContainer}>
          <Text style={styles.emptyText}>{t('recipes.noMatches')}</Text>
          <Text style={styles.emptySubtext}>{t('recipes.noMatchesBlurb')}</Text>
        </View>
      ) : (
        <ScrollView
          style={styles.scrollView}
          contentContainerStyle={styles.scrollContent}
          refreshControl={
            <RefreshControl
              refreshing={lib.refreshing}
              onRefresh={lib.refresh}
              tintColor={theme.text}
              colors={[theme.accent]}
            />
          }
        >
          {visibleRecipes.map(recipe => (
            <RecipeCard
              key={recipe.id}
              recipe={recipe}
              expanded={expandedId === recipe.id}
              variantOfName={recipe.variant_of_recipe_id != null ? recipeNameById.get(recipe.variant_of_recipe_id) : undefined}
              variantCount={variantCountByParent.get(recipe.id) ?? 0}
              onToggle={() => setExpandedId(prev => (prev === recipe.id ? null : recipe.id))}
              onCook={() => navigation.navigate('CookingMode', { recipe })}
              onDelete={async () => {
                if (await lib.remove(recipe.id)) {
                  setExpandedId(prev => (prev === recipe.id ? null : prev));
                }
              }}
              onToggleShare={() => lib.toggleShare(recipe)}
              onExport={() => lib.exportMarkdown([recipe])}
              onVote={v => lib.vote(recipe, v)}
              ensureStructured={lib.ensureStructured}
              onSwap={(sortOrder, replacement) => lib.setSwap(recipe, sortOrder, replacement)}
              onSaveEdit={doc => lib.saveEdit(recipe.id, doc)}
              onRefine={prompt => lib.refine(recipe, prompt)}
              onGenerateVariant={hint => lib.generateVariant(recipe, hint)}
              onAcceptVariant={preview => lib.acceptVariant(preview)}
              onLoadHistory={() => lib.loadHistory(recipe.id)}
              collections={lib.collections}
              onToggleCollection={(collectionId, member) => lib.setRecipeCollection(collectionId, recipe.id, member)}
              onCreateCollection={lib.createCollection}
            />
          ))}
        </ScrollView>
      )}
    </View>
  );
};

// "Deleted 3 days ago" — the trash's only per-row detail, and the one that tells the
// user how long they have left of the 30.
const deletedLabel = (deletedAt: string | undefined, t: (k: string, o?: Record<string, unknown>) => string) => {
  if (!deletedAt) return t('recipes.deleted');
  const days = Math.floor((Date.now() - new Date(deletedAt).getTime()) / 86_400_000);
  if (days <= 0) return t('recipes.deletedToday');
  return t('recipes.deletedDaysAgo', { count: days, left: 30 - days });
};

const makeStyles = (t: Theme) => StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: t.bg,
  },
  scrollView: {
    flex: 1,
  },
  scrollContent: {
    padding: 15,
  },
  trashRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    marginBottom: 10,
  },
  trashText: {
    flex: 1,
  },
  emptyContainer: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 20,
  },
  emptyText: {
    ...type.title,
    color: t.text,
    marginBottom: 10,
  },
  emptySubtext: {
    ...type.body, fontSize: 16,
    color: t.muted,
    textAlign: 'center',
    marginBottom: 30,
  },
  generateButton: {
    backgroundColor: t.accent,
    paddingHorizontal: 30,
    paddingVertical: 15,
    borderRadius: 12,
  },
  generateButtonText: {
    color: t.onAccent,
    ...type.label, fontSize: 16,
  },
  skipButton: {
    marginTop: 15,
    padding: 10,
  },
  skipButtonText: {
    color: t.subtext,
    ...type.label,
    textAlign: 'center',
  },
});

export default RecipesScreen;
