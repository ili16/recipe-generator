import React, { useEffect, useMemo, useState } from 'react';
import { View, Text, StyleSheet, ScrollView, TouchableOpacity } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { Collection, Recipe, RecipeDocument, RecipeVersion } from '../../types';
import { useTheme, Theme } from '../../context/ThemeContext';
import { useLanguage } from '../../context/LanguageContext';
import { type } from '../../theme';
import RecipeView from '../../components/RecipeView';
import { Badge } from '../../components/ui';
import { tagLabelKey, mealTypeSlug } from '../../constants/tags';
import { originLabel } from '../../utils/recipeOrigin';
import { totalTimeMinutes } from '../../utils/recipeTime';
import { makeSharedStyles } from './styles';
import RecipeEditForm from './RecipeEditForm';
import { RefinePanel, VariantPanel, HistoryPanel, CollectionsPanel, VariantPreview } from './RecipePanels';

interface Props {
  recipe: Recipe;
  expanded: boolean;
  variantOfName?: string;
  /** How many saved recipes name this one as their parent (BACKLOG 6.5). */
  variantCount?: number;
  onToggle: () => void;
  onCook: () => void;
  onDelete: () => void;
  /** Share on (copies the link) when unshared, revoke when shared — BACKLOG 8.4. */
  onToggleShare: () => void;
  /** Export this one recipe as Markdown — BACKLOG 13.1. */
  onExport: () => void;
  onVote: (vote: 1 | -1) => void;
  ensureStructured: (recipe: Recipe) => Promise<RecipeDocument | null>;
  /** Swap one ingredient for something the kitchen holds (BACKLOG 17.4c); '' undoes it. */
  onSwap: (sortOrder: number, replacement: string) => void;
  onSaveEdit: (doc: RecipeDocument) => Promise<boolean>;
  onRefine: (prompt: string) => Promise<boolean>;
  onGenerateVariant: (hint: string) => Promise<VariantPreview | null>;
  onAcceptVariant: (preview: VariantPreview) => Promise<boolean>;
  onLoadHistory: () => Promise<RecipeVersion[]>;
  collections: Collection[];
  onToggleCollection: (collectionId: number, member: boolean) => void;
  onCreateCollection: (name: string) => Promise<Collection | null>;
}

type Panel = 'none' | 'edit' | 'refine' | 'variant' | 'history' | 'collections';

// One recipe in the library list: collapsed header, and when expanded the recipe plus
// whichever of the four inline flows the user opened.
const RecipeCard: React.FC<Props> = ({
  recipe, expanded, variantOfName, variantCount = 0, onToggle, onCook, onDelete, onToggleShare, onExport, onVote,
  ensureStructured, onSwap, onSaveEdit, onRefine, onGenerateVariant, onAcceptVariant, onLoadHistory,
  collections, onToggleCollection, onCreateCollection,
}) => {
  const { theme } = useTheme();
  const { t } = useLanguage();
  const s = useMemo(() => makeSharedStyles(theme), [theme]);
  const styles = useMemo(() => makeStyles(theme), [theme]);

  const [panel, setPanel] = useState<Panel>('none');
  const [editDoc, setEditDoc] = useState<RecipeDocument | null>(null);
  const [savingEdit, setSavingEdit] = useState(false);
  const [refineLoading, setRefineLoading] = useState(false);
  const [variantLoading, setVariantLoading] = useState(false);
  const [variantPreview, setVariantPreview] = useState<VariantPreview | null>(null);
  const [historyEntries, setHistoryEntries] = useState<RecipeVersion[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);

  const memberOf = collections.filter(c => c.recipe_ids.includes(recipe.id));
  // Absent on a payload from before 7.3 (and on the meal-plan variant preview), so both
  // default to 0 — which is also what "no pantry match to show" looks like.
  // Outside a household every visible recipe is the caller's, and the server sends no
  // flag on older payloads — so absent means mine, never "somebody else's".
  const mine = recipe.owned_by_me !== false;
  const pantryHave = recipe.pantry_have ?? 0;
  const pantryTotal = recipe.pantry_total ?? 0;
  const pantryClose = recipe.pantry_close ?? 0;
  const mealType = mealTypeSlug(recipe.tags);
  const totalMinutes = totalTimeMinutes(recipe);

  // The list payload has no structured document (it is fetched on demand), and the
  // per-line pantry marks (BACKLOG 16.7) live on it — so opening a card loads it.
  useEffect(() => {
    if (expanded && !recipe.structured) void ensureStructured(recipe);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [expanded, recipe.id]);

  const startEdit = async () => {
    const doc = (await ensureStructured(recipe)) ?? {
      title: recipe.recipename,
      summary: null,
      language: 'en',
      tags: recipe.tags ?? [],
      servings: null,
      total_minutes: null,
      prep_minutes: null,
      cook_minutes: null,
      difficulty: null,
      ingredients: [],
      steps: [],
    };
    setEditDoc(doc);
    setPanel('edit');
  };

  const saveEdit = async (doc: RecipeDocument) => {
    setSavingEdit(true);
    const ok = await onSaveEdit(doc);
    setSavingEdit(false);
    if (ok) {
      setPanel('none');
      setEditDoc(null);
    }
  };

  const runRefine = async (prompt: string) => {
    if (!prompt) return;
    setRefineLoading(true);
    const ok = await onRefine(prompt);
    setRefineLoading(false);
    if (ok) setPanel('none');
  };

  const runVariant = async (hint: string) => {
    setVariantLoading(true);
    setVariantPreview(await onGenerateVariant(hint));
    setVariantLoading(false);
  };

  const acceptVariant = async () => {
    if (!variantPreview) return;
    if (await onAcceptVariant(variantPreview)) {
      setVariantPreview(null);
      setPanel('none');
    }
  };

  const toggleHistory = async () => {
    if (panel === 'history') {
      setPanel('none');
      return;
    }
    setPanel('history');
    setHistoryLoading(true);
    setHistoryEntries(await onLoadHistory());
    setHistoryLoading(false);
  };

  return (
    <View style={styles.recipeCard}>
      <TouchableOpacity onPress={onToggle} style={styles.cardHeader}>
        <View style={styles.cardHeaderMain}>
          <Text style={styles.recipeName}>{recipe.recipename}</Text>
          {recipe.variant_of_recipe_id != null && (
            <Text style={styles.variantOfCaption}>
              {t('recipes.variantOf', { name: variantOfName ?? t('recipes.aSavedRecipe') })}
            </Text>
          )}
          {originLabel(recipe, t) && (
            <Text style={styles.variantOfCaption}>{originLabel(recipe, t)}</Text>
          )}
          {/* What a person scans a list for — which meal, how long, how many calories —
              plus the two badges that are about *this* copy of it (what's in the pantry,
              whether it's shared). Everything else the recipe knows is a filter input and
              agent context, and lives in the expanded card below. */}
          {(mealType || totalMinutes > 0 || recipe.calories != null || !!recipe.share_token || pantryHave > 0) && (
            <View style={styles.tagBadgeRow}>
              {mealType && (
                <Badge
                  label={t(tagLabelKey(mealType))}
                  icon={<Ionicons name="restaurant-outline" size={11} color={theme.accent} />}
                />
              )}
              {totalMinutes > 0 && (
                <Badge
                  label={`${totalMinutes} min`}
                  icon={<Ionicons name="time-outline" size={11} color={theme.accent} />}
                />
              )}
              {recipe.calories != null && (
                <Badge
                  label={`${recipe.calories} kcal`}
                  icon={<Ionicons name="flame-outline" size={11} color={theme.accent} />}
                />
              )}
              {/* Pantry match (BACKLOG 7.3). Only ever shown when the caller actually has
                  something: an empty pantry would otherwise brand every recipe "0/8". */}
              {pantryHave > 0 && (
                <Badge
                  label={pantryHave === pantryTotal
                    ? t('recipes.pantryReady', { count: pantryTotal })
                    : pantryClose > 0
                      // BACKLOG 17.4a: a swap is offered, never counted — the badge says
                      // what the pantry holds and what it could stand in for, separately.
                      ? t('recipes.pantrySwap', { have: pantryHave, total: pantryTotal, count: pantryClose })
                      : t('recipes.pantryPartial', { have: pantryHave, total: pantryTotal })}
                  // BACKLOG 16.7: full coverage gets the one filled badge on the card.
                  tone={pantryHave === pantryTotal ? 'success' : 'accent'}
                  icon={<Ionicons
                    name={pantryHave === pantryTotal ? 'checkmark-circle' : 'file-tray-stacked-outline'}
                    size={11}
                    color={pantryHave === pantryTotal ? theme.onSuccess : theme.accent}
                  />}
                />
              )}
              {!!recipe.share_token && (
                <Badge label={t('recipes.shared')} icon={<Ionicons name="link" size={11} color={theme.accent} />} />
              )}
              {/* Whose recipe this is, shown only when it is not yours — inside a household
                  the library holds everyone's, and a card with no Edit needs to say why
                  (BACKLOG 15.3). */}
              {!mine && (
                <Badge
                  label={t('recipes.ownedBy', { name: recipe.owner_name || t('household.unnamedMember') })}
                  icon={<Ionicons name="people-outline" size={11} color={theme.accent} />}
                />
              )}
            </View>
          )}
        </View>
        <Ionicons
          name={expanded ? 'chevron-down' : 'chevron-forward'}
          size={16}
          color={theme.subtext}
          style={styles.expandIcon}
        />
      </TouchableOpacity>

      {expanded && (
        <View style={styles.cardContent}>
          {panel === 'edit' && editDoc ? (
            <RecipeEditForm
              initial={editDoc}
              saving={savingEdit}
              onCancel={() => { setPanel('none'); setEditDoc(null); }}
              onSave={saveEdit}
            />
          ) : (
            <>
              {/* The rest of what this recipe knows, off the collapsed header so the list
                  stays scannable: tags are filter inputs and agent context, not headlines. */}
              {((recipe.tags ?? []).length > 0 || recipe.manually_edited || memberOf.length > 0 || variantCount > 0) && (
                <View style={styles.detailBadgeRow}>
                  {variantCount > 0 && (
                    <Badge
                      label={t('recipes.variantCount', { count: variantCount })}
                      icon={<Ionicons name="copy-outline" size={11} color={theme.accent} />}
                    />
                  )}
                  {memberOf.map(c => (
                    <Badge
                      key={`c${c.id}`}
                      label={c.name}
                      icon={<Ionicons name="folder-outline" size={11} color={theme.accent} />}
                    />
                  ))}
                  {recipe.manually_edited && (
                    <Badge
                      label={t('recipes.manuallyEdited')}
                      icon={<Ionicons name="create-outline" size={11} color={theme.accent} />}
                    />
                  )}
                  {(recipe.tags ?? []).map(slug => (
                    <Badge key={slug} label={t(tagLabelKey(slug))} tone="neutral" />
                  ))}
                </View>
              )}

              <ScrollView style={styles.recipeContentScroll} nestedScrollEnabled>
                <RecipeView structured={recipe.structured} markdown={recipe.recipe} scalable onSwap={onSwap} />
              </ScrollView>

              {panel === 'refine' && <RefinePanel loading={refineLoading} onApply={runRefine} />}

              {panel === 'variant' && (
                <VariantPanel
                  loading={variantLoading}
                  preview={variantPreview}
                  onGenerate={runVariant}
                  onDiscard={() => setVariantPreview(null)}
                  onAccept={acceptVariant}
                />
              )}

              {panel === 'history' && <HistoryPanel loading={historyLoading} entries={historyEntries} />}

              {panel === 'collections' && (
                <CollectionsPanel
                  collections={collections}
                  recipeId={recipe.id}
                  onToggle={onToggleCollection}
                  onCreate={onCreateCollection}
                />
              )}

              <View style={s.cardActions}>
                {/* Cook, plan, collect and vote work on anybody's recipe; editing,
                    refining, sharing and deleting belong to whoever saved it. A non-owner
                    who wants it changed copies it — that is the variant button, which is
                    why it stays. */}
                {mine && (
                <TouchableOpacity style={s.cookButton} onPress={startEdit}>
                  <Ionicons name="create-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  <Text style={s.cookButtonText}>{t('common.edit')}</Text>
                </TouchableOpacity>
                )}
                {mine && (
                <TouchableOpacity
                  style={s.cookButton}
                  onPress={() => setPanel(p => (p === 'refine' ? 'none' : 'refine'))}
                >
                  <Ionicons name="sparkles-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  <Text style={s.cookButtonText}>{t('recipes.refineWithAi')}</Text>
                </TouchableOpacity>
                )}
                <TouchableOpacity
                  style={s.cookButton}
                  onPress={() => {
                    setVariantPreview(null);
                    setPanel(p => (p === 'variant' ? 'none' : 'variant'));
                  }}
                >
                  <Ionicons name="copy-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  {/* On somebody else's recipe this button *is* the way out of read-only
                      (15.7), so it says so rather than naming the mechanism. */}
                  <Text style={s.cookButtonText}>{t(mine ? 'recipes.createVariant' : 'recipes.makeMyCopy')}</Text>
                </TouchableOpacity>
                <TouchableOpacity
                  style={s.cookButton}
                  onPress={() => setPanel(p => (p === 'collections' ? 'none' : 'collections'))}
                >
                  <Ionicons name="folder-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  <Text style={s.cookButtonText}>{t('recipes.collections')}</Text>
                </TouchableOpacity>
                <TouchableOpacity style={s.cookButton} onPress={toggleHistory}>
                  <Ionicons name="time-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  <Text style={s.cookButtonText}>{t('recipes.history')}</Text>
                </TouchableOpacity>
                <TouchableOpacity style={s.cookButton} onPress={onCook}>
                  <Ionicons name="flame-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  <Text style={s.cookButtonText}>{t('recipes.cook')}</Text>
                </TouchableOpacity>
                {mine && (
                <TouchableOpacity style={s.cookButton} onPress={onToggleShare}>
                  <Ionicons
                    name={recipe.share_token ? 'link' : 'link-outline'}
                    size={14}
                    color={theme.accent}
                    style={{ marginRight: 6 }}
                  />
                  <Text style={s.cookButtonText}>{recipe.share_token ? t('recipes.stopSharing') : t('recipes.shareLink')}</Text>
                </TouchableOpacity>
                )}
                {/* Export is not owner-gated: a household recipe you can read is one
                    you can take a copy of (BACKLOG 13.1). */}
                <TouchableOpacity style={s.cookButton} onPress={onExport}>
                  <Ionicons name="download-outline" size={14} color={theme.accent} style={{ marginRight: 6 }} />
                  <Text style={s.cookButtonText}>{t('recipes.export')}</Text>
                </TouchableOpacity>
                <TouchableOpacity style={s.cookButton} onPress={() => onVote(1)}>
                  <Ionicons name={recipe.my_vote === 1 ? 'thumbs-up' : 'thumbs-up-outline'} size={14} color={theme.accent} />
                </TouchableOpacity>
                <TouchableOpacity style={s.cookButton} onPress={() => onVote(-1)}>
                  <Ionicons name={recipe.my_vote === -1 ? 'thumbs-down' : 'thumbs-down-outline'} size={14} color={theme.accent} />
                </TouchableOpacity>
                {mine && (
                <TouchableOpacity style={s.deleteButton} onPress={onDelete}>
                  <Ionicons name="trash-outline" size={14} color={theme.onAccent} style={{ marginRight: 6 }} />
                  <Text style={s.deleteButtonText}>{t('common.delete')}</Text>
                </TouchableOpacity>
                )}
              </View>
            </>
          )}
        </View>
      )}
    </View>
  );
};

const makeStyles = (t: Theme) => StyleSheet.create({
  recipeCard: {
    backgroundColor: t.surface,
    borderRadius: 12,
    marginBottom: 15,
    borderWidth: 1.5,
    borderColor: t.accent,
  },
  cardHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    padding: 15,
  },
  cardHeaderMain: {
    flex: 1,
  },
  recipeName: {
    ...type.title, fontSize: 18,
    color: t.text,
  },
  variantOfCaption: {
    ...type.caption,
    color: t.subtext,
    marginTop: 2,
  },
  tagBadgeRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 4,
    marginTop: 6,
  },
  detailBadgeRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 4,
    marginBottom: 12,
  },
  expandIcon: {
    ...type.body, fontSize: 16,
    color: t.subtext,
    marginLeft: 10,
  },
  cardContent: {
    borderTopWidth: StyleSheet.hairlineWidth,
    borderTopColor: t.border,
    padding: 15,
  },
  recipeContentScroll: {
    maxHeight: 420,
    marginBottom: 15,
  },
});

export default RecipeCard;
